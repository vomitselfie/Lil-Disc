package secret

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/pbkdf2"
)

const password = "correcthorsebatterystaple"

func passDriver(dir, pass string) *EncryptedFile {
	return EncryptedFileDriver(WithEncryptedFilePath(context.Background(), dir), pass)
}

func saltedDriver(dir string) *EncryptedFile {
	return SaltedFileDriver(WithEncryptedFilePath(context.Background(), dir))
}

var testValues = map[string]string{
	"hello":   "世界",
	"zero":    string(make([]byte, 1024)),
	"a/b+c==": "key with characters base64 std would put in a filename",
}

func TestEncryptedFileDriver(t *testing.T) {
	dir := t.TempDir()
	roundTrip(t, func() *EncryptedFile { return passDriver(dir, password) })
}

func TestSaltedFileDriver(t *testing.T) {
	dir := t.TempDir()
	roundTrip(t, func() *EncryptedFile { return saltedDriver(dir) })
}

func roundTrip(t *testing.T, open func() *EncryptedFile) {
	t.Helper()
	enc := open()
	for k, v := range testValues {
		if err := enc.Set(k, []byte(v)); err != nil {
			t.Fatalf("set %q: %v", k, err)
		}
	}
	// A fresh instance must read what the first one wrote.
	fresh := open()
	for k, v := range testValues {
		b, err := fresh.Get(k)
		if err != nil {
			t.Fatalf("get %q: %v", k, err)
		}
		if string(b) != v {
			t.Fatalf("value mismatch for %q", k)
		}
	}
}

// The regression this format exists for: version 1 wrote the AES key itself
// to .hash. Nothing written now may contain the key.
func TestNoKeyMaterialOnDisk(t *testing.T) {
	dir := t.TempDir()
	if err := passDriver(dir, password).Set("account", []byte("token")); err != nil {
		t.Fatal(err)
	}

	var h header
	b, err := os.ReadFile(filepath.Join(dir, headerFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &h); err != nil {
		t.Fatal(err)
	}
	if h.KDF != kdfArgon2id || len(h.Key) != 0 {
		t.Fatalf("passphrase store header = %+v, want argon2id with no key", h)
	}

	key := argon2.IDKey([]byte(password), h.Salt, h.Time, h.Memory, h.Threads, keySize)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if bytes.Contains(data, key) {
			t.Errorf("%s contains the derived key", e.Name())
		}
		if bytes.Contains(data, []byte(base64.StdEncoding.EncodeToString(key))) {
			t.Errorf("%s contains the derived key, base64-encoded", e.Name())
		}
	}
}

func TestWrongPassword(t *testing.T) {
	dir := t.TempDir()
	if err := passDriver(dir, password).Set("account", []byte("token")); err != nil {
		t.Fatal(err)
	}

	if _, err := passDriver(dir, "wrong").Get("account"); !errors.Is(err, ErrIncorrectPassword) {
		t.Errorf("wrong password: err = %v, want ErrIncorrectPassword", err)
	}
	// Opening a passphrase store without one must not silently succeed.
	if _, err := saltedDriver(dir).Get("account"); !errors.Is(err, ErrIncorrectPassword) {
		t.Errorf("no password: err = %v, want ErrIncorrectPassword", err)
	}
	// And a wrong password must not be able to write under a different key.
	if err := passDriver(dir, "wrong").Set("account", []byte("x")); !errors.Is(err, ErrIncorrectPassword) {
		t.Errorf("set with wrong password: err = %v, want ErrIncorrectPassword", err)
	}
}

// Version 1 returned (nil, nil) for a truncated value, because it wrapped a
// nil error.
func TestTruncatedValueIsAnError(t *testing.T) {
	dir := t.TempDir()
	enc := passDriver(dir, password)
	if err := enc.Set("account", []byte("token")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(enc.valuePath("account"), []byte{1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	if b, err := enc.Get("account"); err == nil {
		t.Fatalf("truncated value: got %q and no error", b)
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	enc := passDriver(dir, password)
	if err := enc.Set("account", []byte("token")); err != nil {
		t.Fatal(err)
	}
	if err := enc.Delete("account"); err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Get("account"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: err = %v, want ErrNotFound", err)
	}
	if err := enc.Delete("account"); err != nil {
		t.Errorf("deleting a missing key: %v", err)
	}
}

// writeV1 builds a version 1 store the way the old code did.
func writeV1(t *testing.T, dir string, pass []byte, values map[string]string) {
	t.Helper()
	salt := bytes.Repeat([]byte{7}, 64)
	if pass == nil {
		pass = salt
	}
	key := pbkdf2.Key(pass, salt, legacyHashRounds, keySize, sha512.New)
	aead, err := newAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, legacySaltFile), salt, 0600))
	must(os.WriteFile(filepath.Join(dir, legacyHashFile), key, 0600))
	for k, v := range values {
		data, err := seal(aead, []byte(v))
		must(err)
		name := base64.RawStdEncoding.EncodeToString([]byte(k))
		must(os.WriteFile(filepath.Join(dir, name), data, 0600))
	}
}

func TestMigrateFromV1(t *testing.T) {
	values := map[string]string{"account": "token", "other": "value"}

	for _, tc := range []struct {
		name string
		pass []byte
		open func(dir string) *EncryptedFile
	}{
		{"passphrase", []byte(password), func(dir string) *EncryptedFile { return passDriver(dir, password) }},
		{"salted", nil, saltedDriver},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeV1(t, dir, tc.pass, values)

			if !IsEncrypted(WithEncryptedFilePath(context.Background(), dir)) {
				t.Fatal("IsEncrypted is false for a version 1 store")
			}

			enc := tc.open(dir)
			for k, v := range values {
				b, err := enc.Get(k)
				if err != nil || string(b) != v {
					t.Fatalf("get %q after migration = %q, %v", k, b, err)
				}
			}

			for _, gone := range []string{legacyHashFile, legacySaltFile} {
				if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
					t.Errorf("%s still present after migration", gone)
				}
			}
			names, _ := enc.legacyValueFiles()
			if len(names) != 0 {
				t.Errorf("version 1 value files left behind: %v", names)
			}

			// And it must still open as version 2.
			if b, err := tc.open(dir).Get("account"); err != nil || string(b) != "token" {
				t.Fatalf("reopen after migration = %q, %v", b, err)
			}
		})
	}
}

func TestMigrateWrongPasswordLeavesV1Intact(t *testing.T) {
	dir := t.TempDir()
	writeV1(t, dir, []byte(password), map[string]string{"account": "token"})

	if _, err := passDriver(dir, "wrong").Get("account"); !errors.Is(err, ErrIncorrectPassword) {
		t.Fatalf("err = %v, want ErrIncorrectPassword", err)
	}
	if _, err := os.Stat(filepath.Join(dir, headerFile)); !os.IsNotExist(err) {
		t.Fatal("a failed unlock wrote a version 2 header")
	}
	if b, err := passDriver(dir, password).Get("account"); err != nil || string(b) != "token" {
		t.Fatalf("right password after a wrong one = %q, %v", b, err)
	}
}

// A crash after the header is written but before the version 1 files are
// removed must not stop the store from opening.
func TestLeftoverV1FilesAfterHeaderAreCleanedUp(t *testing.T) {
	dir := t.TempDir()
	if err := passDriver(dir, password).Set("account", []byte("token")); err != nil {
		t.Fatal(err)
	}
	writeV1(t, dir, []byte(password), map[string]string{"stale": "x"})

	if b, err := passDriver(dir, password).Get("account"); err != nil || string(b) != "token" {
		t.Fatalf("get = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, legacyHashFile)); !os.IsNotExist(err) {
		t.Error(".hash left behind")
	}
}
