package connection

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kayrus/putty"
	"golang.org/x/crypto/ssh"
)

// ErrPassphraseRequired is returned (as a string prefix) when the private key
// is encrypted and no passphrase was given, so the UI can ask for one.
const PassphraseRequiredPrefix = "KEY_PASSPHRASE_REQUIRED"

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

func defaultKeyPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var paths []string
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		p := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	return paths
}

// keyboardInteractivePassword answers keyboard-interactive prompts with the
// saved password, but only prompts that ask for a password without echo.
// Any other question (an OTP code, "type yes to continue"…) makes the method
// fail instead of sending the password where it was not asked for.
func keyboardInteractivePassword(password string) ssh.KeyboardInteractiveChallenge {
	return func(_, _ string, questions []string, echos []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i, q := range questions {
			echo := i < len(echos) && echos[i]
			if echo || !looksLikePasswordPrompt(q) || password == "" {
				return nil, fmt.Errorf("server asked %q, which Terminyxx cannot answer automatically", strings.TrimSpace(q))
			}
			answers[i] = password
		}
		return answers, nil
	}
}

func looksLikePasswordPrompt(q string) bool {
	q = strings.ToLower(q)
	return strings.Contains(q, "password") || strings.Contains(q, "passphrase") || strings.Contains(q, "пароль")
}

// warnKeyPermissions logs (like OpenSSH, but without refusing the key) when
// a private key file can be read by other users.
func warnKeyPermissions(path string) {
	if runtime.GOOS == "windows" {
		return
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		log.Printf("warning: private key %s is accessible by others (mode %04o); run chmod 600 on it", path, fi.Mode().Perm())
	}
}

func loadSigner(path, passphrase string) (ssh.Signer, error) {
	warnKeyPermissions(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", path, err)
	}
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("PuTTY-User-Key-File-")) {
		return loadPuTTYSigner(path, data, passphrase)
	}
	signer, err := ssh.ParsePrivateKey(data)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		if passphrase == "" {
			return nil, fmt.Errorf("%s|%s", PassphraseRequiredPrefix, path)
		}
		signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
	}
	if err != nil {
		return nil, fmt.Errorf("parse key %s: %w", path, err)
	}
	return signer, nil
}

// buildAuthMethods returns the auth methods for req and a cleanup func that
// closes the ssh-agent connection once the handshake is done.
func buildAuthMethods(req ConnectRequest) ([]ssh.AuthMethod, func(), error) {
	var auths []ssh.AuthMethod
	cleanup := func() {}

	if req.AuthType == "key" {
		// Like OpenSSH: an explicit key first, then the agent; without an
		// explicit key, the agent and then the default ~/.ssh keys.
		agentMethod, closeAgent := agentAuth()
		cleanup = closeAgent

		if req.KeyPath != "" {
			s, err := loadSigner(expandHome(req.KeyPath), req.Passphrase)
			if err != nil {
				cleanup()
				return nil, func() {}, err
			}
			auths = append(auths, ssh.PublicKeys(s))
			if agentMethod != nil {
				auths = append(auths, agentMethod)
			}
		} else {
			if agentMethod != nil {
				auths = append(auths, agentMethod)
			}
			var signers []ssh.Signer
			var firstErr error
			for _, p := range defaultKeyPaths() {
				s, err := loadSigner(p, req.Passphrase)
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
				signers = append(signers, s)
			}
			if len(signers) > 0 {
				auths = append(auths, ssh.PublicKeys(signers...))
			}
			if len(auths) == 0 {
				cleanup()
				if firstErr != nil {
					return nil, func() {}, firstErr
				}
				return nil, func() {}, errors.New("no SSH key found: no ssh-agent keys and nothing in ~/.ssh; specify a key path")
			}
		}
	}

	if req.Password != "" || req.AuthType != "key" {
		password := req.Password
		auths = append(auths,
			ssh.Password(password),
			// Many servers only allow keyboard-interactive for passwords.
			ssh.KeyboardInteractive(keyboardInteractivePassword(password)),
		)
	}
	return auths, cleanup, nil
}

// loadPuTTYSigner reads a PuTTY .ppk key (versions 2 and 3, as used by
// PuTTY, WinSCP and KiTTY), encrypted or not.
func loadPuTTYSigner(path string, data []byte, passphrase string) (ssh.Signer, error) {
	k, err := putty.New(data)
	if err != nil {
		return nil, fmt.Errorf("parse key %s: %w", path, err)
	}
	if k.Encryption != "none" && passphrase == "" {
		return nil, fmt.Errorf("%s|%s", PassphraseRequiredPrefix, path)
	}
	raw, err := k.ParseRawPrivateKey([]byte(passphrase))
	if err != nil {
		if k.Encryption != "none" {
			return nil, fmt.Errorf("parse key %s: wrong passphrase (decrypt failed): %w", path, err)
		}
		return nil, fmt.Errorf("parse key %s: %w", path, err)
	}
	if p, ok := raw.(*ed25519.PrivateKey); ok {
		raw = *p
	}
	signer, err := ssh.NewSignerFromKey(raw)
	if err != nil {
		return nil, fmt.Errorf("parse key %s: %w", path, err)
	}
	return signer, nil
}
