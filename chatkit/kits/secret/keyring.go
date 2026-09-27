package secret

import (
	"context"
	"errors"

	"github.com/diamondburned/gotkit/app"
	"github.com/zalando/go-keyring"
)

// Keyring is an implementation of a secret driver using the system's keyring
// driver.
type Keyring struct {
	id string
}

var ErrUnsupportedPlatform = keyring.ErrUnsupportedPlatform

var _ Driver = (*Keyring)(nil)

// KeyringDriver creates a new keyring driver.
func KeyringDriver(ctx context.Context) *Keyring {
	return &Keyring{
		id: app.FromContext(ctx).IDDot("secrets"),
	}
}

// KeyringDriverForID creates a keyring driver for an explicit service ID, such
// as the one an application used before its ID changed.
func KeyringDriverForID(id string) *Keyring {
	return &Keyring{id: id}
}

// IsAvailable returns true if the keyring API is available.
func (k *Keyring) IsAvailable() bool {
	const probe = "__secret_available_000"
	if keyring.Set(k.id, probe, "") != nil {
		return false
	}
	// The probe used to be left behind in the keyring forever.
	keyring.Delete(k.id, probe)
	return true
}

// Set sets the key.
func (k *Keyring) Set(key string, value []byte) error {
	return keyring.Set(k.id, key, string(value))
}

// Get gets the key.
func (k *Keyring) Get(key string) ([]byte, error) {
	v, err := keyring.Get(k.id, key)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return []byte(v), nil
}

// Delete deletes the key. Deleting a key that does not exist is not an error.
func (k *Keyring) Delete(key string) error {
	if err := keyring.Delete(k.id, key); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}
