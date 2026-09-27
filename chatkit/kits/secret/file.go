package secret

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/diamondburned/gotkit/app"
	"github.com/pkg/errors"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/pbkdf2"
)

// On-disk format, version 2.
//
// The directory holds a header, a password check and one file per key:
//
//	.store      JSON header: format version, KDF and its parameters, salt
//	.check      nonce || AES-GCM(checkPlaintext), only for passphrase stores
//	v2-<key>    nonce || AES-GCM(value), key name base64url-encoded
//
// A passphrase is stretched with Argon2id into the AES-256 key. Nothing on
// disk is usable as the key: the passphrase is verified by authenticating
// .check, so a wrong passphrase fails as a GCM authentication error rather
// than by comparing against a stored hash.
//
// A store opened without a passphrase (SaltedFileDriver) keeps a random key
// in the header. That is obfuscation, not protection, exactly as before: with
// no secret supplied by the user there is nothing to protect the key with.
//
// Version 1, which this replaces, derived the AES key with PBKDF2 and then
// wrote that same key to .hash to check the passphrase later, so reading
// .hash was enough to decrypt everything without knowing the passphrase. A
// version 1 store is migrated the first time it is unlocked; see migrate.
const (
	headerFile  = ".store"
	checkFile   = ".check"
	valuePrefix = "v2-"

	formatVersion = 2

	kdfArgon2id = "argon2id"
	kdfNone     = "none"

	checkPlaintext = "lildisc secret store"
)

// Argon2id parameters, following the RFC 9106 second recommended option
// (64 MiB, 3 passes). They are written to the header, so they can change
// without breaking existing stores.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	keySize      = 32
	saltSize     = 32
)

// Version 1 files. Read only during migration, then deleted.
const (
	legacySaltFile   = ".salt"
	legacyHashFile   = ".hash"
	legacyHashRounds = 2 << 19
)

type header struct {
	Version int    `json:"version"`
	KDF     string `json:"kdf"`
	Salt    []byte `json:"salt,omitempty"`
	Time    uint32 `json:"time,omitempty"`
	Memory  uint32 `json:"memory,omitempty"`
	Threads uint8  `json:"threads,omitempty"`
	Key     []byte `json:"key,omitempty"` // kdf "none" only
}

type ctxKey uint8

const (
	_ ctxKey = iota
	encryptedFilePathKey
)

// WithEncryptedFilePath sets the path to be used for the encrypted file. This
// overrides the app's config path.
func WithEncryptedFilePath(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, encryptedFilePathKey, path)
}

func encryptedFilePath(ctx context.Context) string {
	if path, ok := ctx.Value(encryptedFilePathKey).(string); ok {
		return path
	}
	if app := app.FromContext(ctx); app != nil {
		return app.ConfigPath("secrets")
	}
	return os.TempDir()
}

// EncryptedFile is an implementation of a secret driver that encrypts each
// value into its own file.
type EncryptedFile struct {
	path string // directory

	mu   sync.RWMutex
	aead cipher.AEAD

	pass string
	enc  bool
}

var _ Driver = (*EncryptedFile)(nil)

// SaltedFileDriver creates a file driver with no passphrase. The key lives in
// the store's header, so this only keeps values from being read casually.
func SaltedFileDriver(ctx context.Context) *EncryptedFile {
	return &EncryptedFile{path: encryptedFilePath(ctx)}
}

// EncryptedFileDriver creates a file driver protected by the given
// passphrase. An existing store must have been created with the same
// passphrase, or every operation returns ErrIncorrectPassword.
func EncryptedFileDriver(ctx context.Context, passphrase string) *EncryptedFile {
	return &EncryptedFile{path: encryptedFilePath(ctx), pass: passphrase, enc: true}
}

// IsEncrypted returns true if the given context contains an existing store,
// in either format. It is the caller's responsibility to use SaltedFileDriver
// or EncryptedFileDriver on the same path.
func IsEncrypted(ctx context.Context) bool {
	dir := encryptedFilePath(ctx)
	for _, name := range []string{headerFile, legacyHashFile} {
		if f, err := os.Stat(filepath.Join(dir, name)); err == nil && !f.IsDir() {
			return true
		}
	}
	return false
}

// ErrIncorrectPassword is returned if the provided passphrase does not unlock
// the store on disk.
var ErrIncorrectPassword = errors.New("incorrect password")

func (s *EncryptedFile) getAEAD() (cipher.AEAD, error) {
	s.mu.RLock()
	aead := s.aead
	s.mu.RUnlock()

	if aead != nil {
		return aead, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.aead != nil {
		return s.aead, nil
	}

	aead, err := s.unlock()
	if err != nil {
		return nil, err
	}

	s.pass = "" // no longer needed
	s.aead = aead
	return aead, nil
}

// unlock opens the store, creating it or migrating it from version 1 first
// if needed. It must be called with s.mu held.
func (s *EncryptedFile) unlock() (cipher.AEAD, error) {
	if err := os.MkdirAll(s.path, 0700); err != nil {
		return nil, errors.Wrap(err, "failed to mkdir -p")
	}

	h, err := s.readHeader()
	switch {
	case err == nil:
		aead, err := s.open(h)
		if err != nil {
			return nil, err
		}
		// A migration that crashed after writing the header leaves the
		// version 1 files behind; they are unreadable now, so drop them.
		s.removeLegacy()
		return aead, nil

	case !os.IsNotExist(errors.Cause(err)):
		return nil, err
	}

	if _, err := os.Stat(filepath.Join(s.path, legacyHashFile)); err == nil {
		return s.migrate()
	}

	return s.create()
}

func (s *EncryptedFile) readHeader() (*header, error) {
	b, err := os.ReadFile(filepath.Join(s.path, headerFile))
	if err != nil {
		return nil, err
	}

	var h header
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, errors.Wrap(err, "corrupt secret store header")
	}
	if h.Version != formatVersion {
		return nil, errors.Errorf("unsupported secret store version %d", h.Version)
	}
	return &h, nil
}

// open derives the key described by h and checks it.
func (s *EncryptedFile) open(h *header) (cipher.AEAD, error) {
	switch h.KDF {
	case kdfNone:
		if s.enc {
			return nil, ErrIncorrectPassword
		}
		return newAEAD(h.Key)

	case kdfArgon2id:
		if !s.enc {
			return nil, ErrIncorrectPassword
		}
		key := argon2.IDKey([]byte(s.pass), h.Salt, h.Time, h.Memory, h.Threads, keySize)
		aead, err := newAEAD(key)
		if err != nil {
			return nil, err
		}

		check, err := os.ReadFile(filepath.Join(s.path, checkFile))
		if err != nil {
			return nil, errors.Wrap(err, "failed to read password check")
		}
		plain, err := openSealed(aead, check)
		if err != nil || subtle.ConstantTimeCompare(plain, []byte(checkPlaintext)) != 1 {
			return nil, ErrIncorrectPassword
		}
		return aead, nil

	default:
		return nil, errors.Errorf("unknown secret store KDF %q", h.KDF)
	}
}

// create initialises an empty store.
func (s *EncryptedFile) create() (cipher.AEAD, error) {
	h, aead, err := s.newHeader()
	if err != nil {
		return nil, err
	}
	if err := s.commitHeader(h, aead); err != nil {
		return nil, err
	}
	return aead, nil
}

// newHeader generates a header and its key for this driver's mode.
func (s *EncryptedFile) newHeader() (*header, cipher.AEAD, error) {
	if !s.enc {
		key, err := randomBytes(keySize)
		if err != nil {
			return nil, nil, err
		}
		aead, err := newAEAD(key)
		if err != nil {
			return nil, nil, err
		}
		return &header{Version: formatVersion, KDF: kdfNone, Key: key}, aead, nil
	}

	salt, err := randomBytes(saltSize)
	if err != nil {
		return nil, nil, err
	}
	h := &header{
		Version: formatVersion,
		KDF:     kdfArgon2id,
		Salt:    salt,
		Time:    argonTime,
		Memory:  argonMemory,
		Threads: argonThreads,
	}
	aead, err := newAEAD(argon2.IDKey([]byte(s.pass), salt, h.Time, h.Memory, h.Threads, keySize))
	if err != nil {
		return nil, nil, err
	}
	return h, aead, nil
}

// commitHeader writes the password check, then the header. The header is
// written last because its presence is what marks the store as version 2.
func (s *EncryptedFile) commitHeader(h *header, aead cipher.AEAD) error {
	if h.KDF == kdfArgon2id {
		check, err := seal(aead, []byte(checkPlaintext))
		if err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Join(s.path, checkFile), check); err != nil {
			return errors.Wrap(err, "failed to write password check")
		}
	}

	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(s.path, headerFile), b); err != nil {
		return errors.Wrap(err, "failed to write secret store header")
	}
	return nil
}

// migrate unlocks a version 1 store and rewrites it as version 2.
//
// Values are re-encrypted into v2- files alongside the old ones, then the
// header is written, then the version 1 files are deleted. A crash before
// the header leaves version 1 intact and the migration simply reruns; a
// crash after it leaves version 2 complete, and the leftovers are removed on
// the next unlock.
func (s *EncryptedFile) migrate() (cipher.AEAD, error) {
	salt, err := os.ReadFile(filepath.Join(s.path, legacySaltFile))
	if err != nil {
		return nil, errors.Wrap(err, "missing salt file")
	}
	hash, err := os.ReadFile(filepath.Join(s.path, legacyHashFile))
	if err != nil {
		return nil, errors.Wrap(err, "failed to read old hash")
	}

	password := salt
	if s.enc {
		password = []byte(s.pass)
	}
	legacyKey := pbkdf2.Key(password, salt, legacyHashRounds, keySize, sha512.New)
	if subtle.ConstantTimeCompare(legacyKey, hash) != 1 {
		return nil, ErrIncorrectPassword
	}
	legacy, err := newAEAD(legacyKey)
	if err != nil {
		return nil, err
	}

	h, aead, err := s.newHeader()
	if err != nil {
		return nil, err
	}

	names, err := s.legacyValueFiles()
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		key, err := base64.RawStdEncoding.DecodeString(name)
		if err != nil {
			continue
		}
		sealed, err := os.ReadFile(filepath.Join(s.path, name))
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read %q for migration", key)
		}
		value, err := openSealed(legacy, sealed)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to decrypt %q for migration", key)
		}
		if err := s.writeValue(aead, string(key), value); err != nil {
			return nil, err
		}
	}

	if err := s.commitHeader(h, aead); err != nil {
		return nil, err
	}
	s.removeLegacy()
	return aead, nil
}

// legacyValueFiles lists version 1 value files: everything that is not a
// dotfile, a version 2 file or a temporary file.
func (s *EncryptedFile) legacyValueFiles() ([]string, error) {
	entries, err := os.ReadDir(s.path)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list secret store")
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || strings.HasPrefix(name, valuePrefix) ||
			strings.HasSuffix(name, ".tmp") {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

func (s *EncryptedFile) removeLegacy() {
	names, _ := s.legacyValueFiles()
	for _, name := range append(names, legacySaltFile, legacyHashFile) {
		os.Remove(filepath.Join(s.path, name))
	}
}

// IsAvailable returns true if the encryption can initialize itself.
func (s *EncryptedFile) IsAvailable() bool {
	return s.Initialize() == nil
}

// Initialize initializes the encryption. Once it returns a nil error, all
// future calls on that instance will always do nothing and return nil.
func (s *EncryptedFile) Initialize() error {
	_, err := s.getAEAD()
	return err
}

func (s *EncryptedFile) valuePath(key string) string {
	return filepath.Join(s.path, valuePrefix+base64.RawURLEncoding.EncodeToString([]byte(key)))
}

func (s *EncryptedFile) writeValue(aead cipher.AEAD, key string, value []byte) error {
	data, err := seal(aead, value)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.valuePath(key), data); err != nil {
		return errors.Wrap(err, "failed to write value to file")
	}
	return nil
}

func (s *EncryptedFile) Set(key string, value []byte) error {
	aead, err := s.getAEAD()
	if err != nil {
		return errors.Wrap(err, "failed to get cipher")
	}
	return s.writeValue(aead, key, value)
}

func (s *EncryptedFile) Get(key string) ([]byte, error) {
	aead, err := s.getAEAD()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cipher")
	}

	b, err := os.ReadFile(s.valuePath(key))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, errors.Wrap(err, "failed to get key")
	}

	value, err := openSealed(aead, b)
	if err != nil {
		return nil, errors.Wrap(err, "decryption error")
	}
	return value, nil
}

// Delete removes the key. Deleting a key that does not exist is not an error.
func (s *EncryptedFile) Delete(key string) error {
	if err := os.Remove(s.valuePath(key)); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to delete key")
	}
	return nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create AES cipher")
	}
	gcm, err := cipher.NewGCM(c)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create GCM cipherer")
	}
	return gcm, nil
}

// seal returns nonce || ciphertext.
func seal(aead cipher.AEAD, plain []byte) ([]byte, error) {
	nonce, err := randomBytes(aead.NonceSize())
	if err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, nil), nil
}

// openSealed reverses seal.
func openSealed(aead cipher.AEAD, b []byte) ([]byte, error) {
	if len(b) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("invalid file content: too short")
	}
	n := aead.NonceSize()
	return aead.Open(nil, b[:n], b[n:], nil)
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, errors.Wrap(err, "failed to read random bytes")
	}
	return b, nil
}

// writeFileAtomic writes data to a temporary file in the same directory and
// renames it into place, so a crash never leaves a truncated file.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename

	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
