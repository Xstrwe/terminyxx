//go:build darwin && cgo

package store

import (
	"errors"

	"github.com/keybase/go-keychain"
	"github.com/zalando/go-keyring"
)

// nativeMarker tags Keychain items written through the Security framework by
// the app itself. Older versions stored the same service/account through
// go-keyring, which shells out to /usr/bin/security: such items trust that
// binary, so any process of the user could read them with
// `security find-generic-password -w` without a prompt. Items created by the
// app process are bound to the app's code signature instead.
const nativeMarker = "terminyxx-native-v1"

// macKeychain stores secrets in the login Keychain via the Security framework.
// Items written by older versions are migrated on first read.
type macKeychain struct{}

func newOSKeyring() Secrets { return macKeychain{} }

func nativeQuery(key string) keychain.Item {
	q := keychain.NewItem()
	q.SetSecClass(keychain.SecClassGenericPassword)
	q.SetService(keyringService)
	q.SetAccount(key)
	q.SetDescription(nativeMarker)
	return q
}

func (macKeychain) getNative(key string) (string, bool, error) {
	q := nativeQuery(key)
	q.SetMatchLimit(keychain.MatchLimitOne)
	q.SetReturnData(true)
	res, err := keychain.QueryItem(q)
	if err != nil {
		return "", false, err
	}
	if len(res) == 0 {
		return "", false, nil
	}
	return string(res[0].Data), true, nil
}

func (macKeychain) addNative(key, value string) error {
	item := keychain.NewGenericPassword(keyringService, key, "Terminyxx", []byte(value), "")
	item.SetDescription(nativeMarker)
	item.SetSynchronizable(keychain.SynchronizableNo)
	item.SetAccessible(keychain.AccessibleWhenUnlocked)
	return keychain.AddItem(item)
}

func (k macKeychain) Get(key string) (string, error) {
	v, ok, err := k.getNative(key)
	if err != nil {
		return "", err
	}
	if ok {
		return v, nil
	}
	// Legacy item written via /usr/bin/security: read it with the same tool
	// (it is on that item's ACL, so no prompt), then recreate it natively.
	v, err = keyring.Get(keyringService, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrSecretNotFound
	}
	if err != nil {
		return "", err
	}
	if delErr := keyring.Delete(keyringService, key); delErr == nil {
		if addErr := k.addNative(key, v); addErr != nil {
			// Never lose the secret: put the legacy item back.
			_ = keyring.Set(keyringService, key, v)
		}
	}
	return v, nil
}

func (k macKeychain) Set(key, value string) error {
	upd := keychain.NewItem()
	upd.SetData([]byte(value))
	err := keychain.UpdateItem(nativeQuery(key), upd)
	if err == nil {
		return nil
	}
	if !errors.Is(err, keychain.ErrorItemNotFound) {
		return err
	}
	// A legacy item with the same service/account would collide with the new
	// one, and it should not keep a copy readable by /usr/bin/security.
	if delErr := keyring.Delete(keyringService, key); delErr != nil && !errors.Is(delErr, keyring.ErrNotFound) {
		return delErr
	}
	return k.addNative(key, value)
}

func (macKeychain) Delete(key string) error {
	err := keychain.DeleteItem(nativeQuery(key))
	if err != nil && !errors.Is(err, keychain.ErrorItemNotFound) {
		return err
	}
	legacyErr := keyring.Delete(keyringService, key)
	if legacyErr != nil && !errors.Is(legacyErr, keyring.ErrNotFound) {
		return legacyErr
	}
	return nil
}
