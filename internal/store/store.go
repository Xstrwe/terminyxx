package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
	"terminyxx/internal/rdp"
)

type SSHProfile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	AuthType string `json:"authType"`
	// Password is write-only: set it to store a new password (kept in the
	// OS keychain). Lists always return it empty; see HasPassword.
	Password      string `json:"password"`
	HasPassword   bool   `json:"hasPassword"`
	ClearPassword bool   `json:"clearPassword"`
	KeyPath       string `json:"keyPath"`
	FolderID      string `json:"folderId"`
	SortOrder     int    `json:"sortOrder"`
	// AllowLegacyAlgorithms lets this host use SHA-1 / CBC algorithms when
	// the global "modern algorithms only" setting is on.
	AllowLegacyAlgorithms bool `json:"allowLegacyAlgorithms"`
	// AgentForwarding forwards the local ssh-agent to this host (only when
	// the global setting allows agent forwarding).
	AgentForwarding bool `json:"agentForwarding"`
	// JumpProfileID is another saved host to connect through (ProxyJump).
	JumpProfileID string `json:"jumpProfileId"`
	// Tunnels are port forwards the user saved for this host.
	Tunnels []Tunnel `json:"tunnels"`
	// SFTPOnly marks a host that only offers SFTP (no shell): the UI opens the
	// file manager and never starts a terminal.
	SFTPOnly bool `json:"sftpOnly"`
}

// Tunnel is a saved port forward (same shape as connection.TunnelSpec).
type Tunnel struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	BindHost  string `json:"bindHost"`
	BindPort  int    `json:"bindPort"`
	DestHost  string `json:"destHost"`
	DestPort  int    `json:"destPort"`
	AutoStart bool   `json:"autoStart"`
}

type Folder struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ParentID  string `json:"parentId"`
	Color     string `json:"color"`
	SortOrder int    `json:"sortOrder"`
}

type Store struct {
	db      *sql.DB
	secrets Secrets
}

// New opens the app database in the user's config directory.
func New(secrets Secrets) (*Store, error) {
	appDir, err := DataDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(appDir, 0o700); err != nil {
		return nil, err
	}
	// Older versions created the folder 0755; it holds the database,
	// known_hosts and session recordings, so keep it private to the user.
	if err := os.Chmod(appDir, 0o700); err != nil {
		log.Printf("store: chmod %s: %v", appDir, err)
	}
	return Open(filepath.Join(appDir, "terminyxx.db"), secrets)
}

// DataDir is where the app keeps its database and known_hosts:
// $TERMINYXX_DATA_DIR if set (handy for development), else
// <user config dir>/terminyxx.
func DataDir() (string, error) {
	if dir := os.Getenv("TERMINYXX_DATA_DIR"); dir != "" {
		return dir, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "terminyxx"), nil
}

// Open opens (and migrates) the database at path.
func Open(path string, secrets Secrets) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: serialize writes, avoid SQLITE_BUSY
	s := &Store{db: db, secrets: secrets}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	s.moveLegacyPasswords()
	restrictDBFiles(path)
	return s, nil
}

// restrictDBFiles makes the database (and its journal files) readable by the
// current user only. Older versions left them at the umask default (0644).
func restrictDBFiles(path string) {
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return
	}
	for _, f := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		if err := os.Chmod(f, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("store: chmod %s: %v", f, err)
		}
	}
}

// ErrKeychainUnavailable is returned when a password could not be stored in
// the OS keychain. Passwords are never written to the database in plaintext:
// the rest of the profile is saved and the password is left unchanged.
var ErrKeychainUnavailable = errors.New("the OS keychain is unavailable, so the password was not saved (the rest of the host was saved)")

func (s *Store) Close() error {
	return s.db.Close()
}

// migrations[i] upgrades the schema from version i to i+1
// (PRAGMA user_version). Never edit a released migration; append a new one.
var migrations = []string{
	// v1: initial schema (also matches databases created before versioning).
	`
CREATE TABLE IF NOT EXISTS ssh_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL DEFAULT 22,
    username TEXT NOT NULL,
    auth_type TEXT NOT NULL DEFAULT 'password',
    password TEXT NOT NULL DEFAULT '',
    key_path TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS rdp_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL DEFAULT 3389,
    username TEXT NOT NULL DEFAULT '',
    domain TEXT NOT NULL DEFAULT '',
    password TEXT NOT NULL DEFAULT '',
    width INTEGER NOT NULL DEFAULT 1920,
    height INTEGER NOT NULL DEFAULT 1080,
    fullscreen INTEGER NOT NULL DEFAULT 0
);`,
	// v2: folders and manual ordering.
	`
CREATE TABLE folders (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    parent_id TEXT NOT NULL DEFAULT '',
    color TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0
);
ALTER TABLE ssh_profiles ADD COLUMN folder_id TEXT NOT NULL DEFAULT '';
ALTER TABLE ssh_profiles ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;
ALTER TABLE rdp_profiles ADD COLUMN folder_id TEXT NOT NULL DEFAULT '';
ALTER TABLE rdp_profiles ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;
UPDATE ssh_profiles SET sort_order = (SELECT COUNT(*) FROM ssh_profiles p2 WHERE p2.name < ssh_profiles.name);
UPDATE rdp_profiles SET sort_order = 1000 + (SELECT COUNT(*) FROM rdp_profiles p2 WHERE p2.name < rdp_profiles.name);`,
	// v3: app settings (key/value, values are JSON written by the frontend).
	`
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);`,
	// v4: per-host SSH hardening overrides.
	`
ALTER TABLE ssh_profiles ADD COLUMN allow_legacy_algorithms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ssh_profiles ADD COLUMN agent_forwarding INTEGER NOT NULL DEFAULT 0;`,
		// v5: RDP session options (JSON; empty = defaults).
		`
ALTER TABLE rdp_profiles ADD COLUMN options TEXT NOT NULL DEFAULT '';`,
		// v6: jump host and saved tunnels per SSH host.
		`
ALTER TABLE ssh_profiles ADD COLUMN jump_profile_id TEXT NOT NULL DEFAULT '';
ALTER TABLE ssh_profiles ADD COLUMN tunnels TEXT NOT NULL DEFAULT '';`,
		// v7: command snippets.
		`
CREATE TABLE snippets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    command TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    grp TEXT NOT NULL DEFAULT '',
    execute INTEGER NOT NULL DEFAULT 1,
    sort_order INTEGER NOT NULL DEFAULT 0
);`,
	// v8: SFTP-only hosts (no shell).
	`
ALTER TABLE ssh_profiles ADD COLUMN sftp_only INTEGER NOT NULL DEFAULT 0;`,
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	for v := version; v < len(migrations); v++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", v+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// moveLegacyPasswords moves plaintext passwords from the database into the
// keychain. If the keychain is unavailable they stay where they are.
func (s *Store) moveLegacyPasswords() {
	if s.secrets == nil {
		return
	}
	rows, err := s.db.Query(`SELECT id, password FROM ssh_profiles WHERE password != ''`)
	if err != nil {
		return
	}
	type pw struct{ id, value string }
	var pending []pw
	for rows.Next() {
		var p pw
		if rows.Scan(&p.id, &p.value) == nil {
			pending = append(pending, p)
		}
	}
	rows.Close()
	for _, p := range pending {
		if err := s.secrets.Set(sshSecretKey(p.id), p.value); err != nil {
			log.Printf("store: keychain unavailable, keeping password in database: %v", err)
			return
		}
		_, _ = s.db.Exec(`UPDATE ssh_profiles SET password = '' WHERE id = ?`, p.id)
	}
}

// --- SSH profiles ---

const sshColumns = `id, name, host, port, username, auth_type, password, key_path, folder_id, sort_order, allow_legacy_algorithms, agent_forwarding, jump_profile_id, tunnels, sftp_only`

func scanSSH(row interface{ Scan(...any) error }) (SSHProfile, string, error) {
	var p SSHProfile
	var dbPassword string
	var legacy, agentFwd, sftpOnly int
	var tunnels string
	err := row.Scan(&p.ID, &p.Name, &p.Host, &p.Port, &p.Username, &p.AuthType, &dbPassword, &p.KeyPath, &p.FolderID, &p.SortOrder, &legacy, &agentFwd, &p.JumpProfileID, &tunnels, &sftpOnly)
	p.SFTPOnly = sftpOnly == 1
	p.Tunnels = []Tunnel{}
	if tunnels != "" {
		_ = json.Unmarshal([]byte(tunnels), &p.Tunnels)
	}
	p.AllowLegacyAlgorithms = legacy == 1
	p.AgentForwarding = agentFwd == 1
	return p, dbPassword, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) ListSSHProfiles() ([]SSHProfile, error) {
	rows, err := s.db.Query(`SELECT ` + sshColumns + ` FROM ssh_profiles ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	profiles := []SSHProfile{}
	for rows.Next() {
		p, dbPassword, err := scanSSH(rows)
		if err != nil {
			return nil, err
		}
		p.HasPassword = dbPassword != "" || s.hasSecret(p.ID)
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

func (s *Store) hasSecret(id string) bool {
	if s.secrets == nil {
		return false
	}
	_, err := s.secrets.Get(sshSecretKey(id))
	return err == nil
}

// SSHPassword returns the stored password for a profile ("" if none).
func (s *Store) SSHPassword(id string) (string, error) {
	if s.secrets != nil {
		v, err := s.secrets.Get(sshSecretKey(id))
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, ErrSecretNotFound) {
			log.Printf("store: keychain read failed: %v", err)
		}
	}
	var pw string
	err := s.db.QueryRow(`SELECT password FROM ssh_profiles WHERE id = ?`, id).Scan(&pw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return pw, err
}

func (s *Store) SaveSSHProfile(p SSHProfile) error {
	var exists bool
	var sortOrder int
	err := s.db.QueryRow(`SELECT sort_order FROM ssh_profiles WHERE id = ?`, p.ID).Scan(&sortOrder)
	switch {
	case err == nil:
		exists = true
	case errors.Is(err, sql.ErrNoRows):
		sortOrder = s.nextHostOrder(p.FolderID)
	default:
		return err
	}

	// The password goes to the keychain only. The DB column is legacy: it
	// is still read for old databases but never written with a new password.
	var pwErr error
	if p.Password != "" {
		if s.secrets == nil {
			pwErr = ErrKeychainUnavailable
		} else if err := s.secrets.Set(sshSecretKey(p.ID), p.Password); err != nil {
			log.Printf("store: keychain write failed: %v", err)
			pwErr = ErrKeychainUnavailable
		}
	}

	if p.JumpProfileID == p.ID {
		p.JumpProfileID = ""
	}
	if p.Tunnels == nil {
		p.Tunnels = []Tunnel{}
	}
	tunnelsJSON, err := json.Marshal(p.Tunnels)
	if err != nil {
		return err
	}

	if exists {
		_, err = s.db.Exec(`
UPDATE ssh_profiles SET name=?, host=?, port=?, username=?, auth_type=?, key_path=?, folder_id=?,
    allow_legacy_algorithms=?, agent_forwarding=?, jump_profile_id=?, tunnels=?, sftp_only=?
WHERE id=?`, p.Name, p.Host, p.Port, p.Username, p.AuthType, p.KeyPath, p.FolderID,
			boolInt(p.AllowLegacyAlgorithms), boolInt(p.AgentForwarding), p.JumpProfileID, string(tunnelsJSON), boolInt(p.SFTPOnly), p.ID)
		if err != nil {
			return err
		}
		switch {
		case p.Password != "" && pwErr == nil:
			// The new password is in the keychain: drop any legacy copy.
			_, err = s.db.Exec(`UPDATE ssh_profiles SET password='' WHERE id=?`, p.ID)
		case p.ClearPassword:
			s.deleteSecret(p.ID)
			_, err = s.db.Exec(`UPDATE ssh_profiles SET password='' WHERE id=?`, p.ID)
		}
		if err != nil {
			return err
		}
		return pwErr
	}

	_, err = s.db.Exec(`INSERT INTO ssh_profiles (`+sshColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Host, p.Port, p.Username, p.AuthType, "", p.KeyPath, p.FolderID, sortOrder,
		boolInt(p.AllowLegacyAlgorithms), boolInt(p.AgentForwarding), p.JumpProfileID, string(tunnelsJSON), boolInt(p.SFTPOnly))
	if err != nil {
		return err
	}
	return pwErr
}

func (s *Store) deleteSecret(id string) {
	if s.secrets != nil {
		_ = s.secrets.Delete(sshSecretKey(id))
	}
}

func (s *Store) DeleteSSHProfile(id string) error {
	s.deleteSecret(id)
	_, err := s.db.Exec(`DELETE FROM ssh_profiles WHERE id = ?`, id)
	return err
}

func (s *Store) GetSSHProfile(id string) (SSHProfile, error) {
	p, dbPassword, err := scanSSH(s.db.QueryRow(`SELECT `+sshColumns+` FROM ssh_profiles WHERE id = ?`, id))
	if err != nil {
		return p, fmt.Errorf("get ssh profile: %w", err)
	}
	p.HasPassword = dbPassword != "" || s.hasSecret(p.ID)
	return p, nil
}

// --- RDP profiles ---

const rdpColumns = `id, name, host, port, username, domain, password, width, height, fullscreen, folder_id, sort_order, options`

func rdpSecretKey(id string) string { return "rdp:" + id }

func scanRDP(row interface{ Scan(...any) error }) (rdp.RDPProfile, error) {
	var p rdp.RDPProfile
	var fullscreen int
	var options string
	err := row.Scan(&p.ID, &p.Name, &p.Host, &p.Port, &p.Username, &p.Domain, &p.Password, &p.Width, &p.Height, &fullscreen, &p.FolderID, &p.SortOrder, &options)
	p.Fullscreen = fullscreen == 1
	p.Options = rdp.DefaultOptions()
	if options != "" {
		if json.Unmarshal([]byte(options), &p.Options) != nil {
			p.Options = rdp.DefaultOptions()
		}
	}
	if p.Options.DrivePaths == nil {
		p.Options.DrivePaths = []string{}
	}
	return p, err
}

// fillRDPPassword blanks the password (write-only for the UI) and flags
// whether one is stored. With reveal it returns the real value instead.
func (s *Store) fillRDPPassword(p *rdp.RDPProfile, reveal bool) {
	dbPassword := p.Password
	p.Password = ""
	stored := ""
	if s.secrets != nil {
		if v, err := s.secrets.Get(rdpSecretKey(p.ID)); err == nil {
			stored = v
		}
	}
	if stored == "" {
		stored = dbPassword
	}
	p.HasPassword = stored != ""
	if reveal {
		p.Password = stored
	}
}

func (s *Store) ListRDPProfiles() ([]rdp.RDPProfile, error) {
	rows, err := s.db.Query(`SELECT ` + rdpColumns + ` FROM rdp_profiles ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	profiles := []rdp.RDPProfile{}
	for rows.Next() {
		p, err := scanRDP(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range profiles {
		s.fillRDPPassword(&profiles[i], false)
	}
	return profiles, nil
}

func (s *Store) SaveRDPProfile(p rdp.RDPProfile) error {
	fullscreen := 0
	if p.Fullscreen {
		fullscreen = 1
	}
	var sortOrder int
	var oldPassword string
	err := s.db.QueryRow(`SELECT sort_order, password FROM rdp_profiles WHERE id = ?`, p.ID).Scan(&sortOrder, &oldPassword)
	if errors.Is(err, sql.ErrNoRows) {
		sortOrder = s.nextHostOrder(p.FolderID)
	} else if err != nil {
		return err
	}

	// A new password goes to the keychain only; the DB column is a legacy
	// value that is kept (never rewritten) until a keychain copy replaces it.
	dbPassword := oldPassword
	var pwErr error
	switch {
	case p.Password != "":
		if s.secrets == nil {
			pwErr = ErrKeychainUnavailable
		} else if err := s.secrets.Set(rdpSecretKey(p.ID), p.Password); err != nil {
			log.Printf("store: keychain write failed: %v", err)
			pwErr = ErrKeychainUnavailable
		} else {
			dbPassword = ""
		}
	case p.ClearPassword:
		dbPassword = ""
		if s.secrets != nil {
			_ = s.secrets.Delete(rdpSecretKey(p.ID))
		}
	}

	if p.Options.DrivePaths == nil {
		p.Options.DrivePaths = []string{}
	}
	opts, err := json.Marshal(p.Options)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
INSERT INTO rdp_profiles (`+rdpColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    name=excluded.name, host=excluded.host, port=excluded.port, username=excluded.username,
    domain=excluded.domain, password=excluded.password, width=excluded.width, height=excluded.height,
    fullscreen=excluded.fullscreen, folder_id=excluded.folder_id, options=excluded.options
`, p.ID, p.Name, p.Host, p.Port, p.Username, p.Domain, dbPassword, p.Width, p.Height, fullscreen, p.FolderID, sortOrder, string(opts))
	if err != nil {
		return err
	}
	return pwErr
}

func (s *Store) DeleteRDPProfile(id string) error {
	if s.secrets != nil {
		_ = s.secrets.Delete(rdpSecretKey(id))
	}
	_, err := s.db.Exec(`DELETE FROM rdp_profiles WHERE id = ?`, id)
	return err
}

// GetRDPProfile returns the profile with its real password filled in, for
// launching. Never hand the result to the UI.
func (s *Store) GetRDPProfile(id string) (rdp.RDPProfile, error) {
	p, err := scanRDP(s.db.QueryRow(`SELECT `+rdpColumns+` FROM rdp_profiles WHERE id = ?`, id))
	if err != nil {
		return p, fmt.Errorf("get rdp profile: %w", err)
	}
	s.fillRDPPassword(&p, true)
	return p, nil
}
