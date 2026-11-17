//go:build !darwin || !cgo

package store

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// goKeyring stores secrets via go-keyring: Windows Credential Manager or the
// Linux Secret Service.
type goKeyring struct{}

func newOSKeyring() Secrets { return goKeyring{} }

func (goKeyring) Get(key string) (string, error) {
	v, err := keyring.Get(keyringService, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrSecretNotFound
	}
	return v, err
}

func (goKeyring) Set(key, value string) error { return keyring.Set(keyringService, key, value) }

func (goKeyring) Delete(key string) error {
	err := keyring.Delete(keyringService, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
