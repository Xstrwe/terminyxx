package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"terminyxx/internal/rdp"
)

func openTest(t *testing.T) (*Store, *MemorySecrets, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	sec := NewMemorySecrets()
	s, err := Open(path, sec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, sec, path
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// A database created by the pre-versioning app (plaintext passwords, no
// folder columns) must upgrade without losing data.
func TestMigrateLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	must(t, err)
	_, err = db.Exec(migrations[0] + `
INSERT INTO ssh_profiles (id, name, host, port, username, auth_type, password, key_path)
VALUES ('a', 'Zeta', 'z.example', 22, 'root', 'password', 'hunter2', ''),
       ('b', 'Alpha', 'a.example', 2222, 'deploy', 'key', '', '~/.ssh/id_ed25519');
INSERT INTO rdp_profiles (id, name, host) VALUES ('r', 'Office', 'office.local');`)
	must(t, err)
	db.Close()

	sec := NewMemorySecrets()
	s, err := Open(path, sec)
	must(t, err)
	defer s.Close()

	var version int
	must(t, s.db.QueryRow(`PRAGMA user_version`).Scan(&version))
	if version != len(migrations) {
		t.Fatalf("user_version = %d, want %d", version, len(migrations))
	}

	list, err := s.ListSSHProfiles()
	must(t, err)
	if len(list) != 2 || list[0].Name != "Alpha" || list[1].Name != "Zeta" {
		t.Fatalf("profiles not migrated/ordered by name: %+v", list)
	}
	if list[1].Password != "" || !list[1].HasPassword || list[0].HasPassword {
		t.Fatalf("password flags wrong: %+v", list)
	}
	if v, _ := sec.Get("ssh:a"); v != "hunter2" {
		t.Fatalf("password not moved to keychain: %q", v)
	}
	var col string
	must(t, s.db.QueryRow(`SELECT password FROM ssh_profiles WHERE id='a'`).Scan(&col))
	if col != "" {
		t.Fatal("plaintext password left in database")
	}
	if pw, _ := s.SSHPassword("a"); pw != "hunter2" {
		t.Fatalf("SSHPassword = %q", pw)
	}
	r, err := s.ListRDPProfiles()
	must(t, err)
	if len(r) != 1 || r[0].Name != "Office" {
		t.Fatalf("rdp not migrated: %+v", r)
	}

	// Re-opening is a no-op.
	s.Close()
	s2, err := Open(path, sec)
	must(t, err)
	s2.Close()
}

type failingSecrets struct{ *MemorySecrets }

func (*failingSecrets) Set(string, string) error { return errors.New("no keychain") }

func TestNoPlaintextPasswordWithoutKeychain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(path, &failingSecrets{NewMemorySecrets()})
	must(t, err)
	defer s.Close()
	err = s.SaveSSHProfile(SSHProfile{ID: "x", Name: "x", Host: "h", Port: 22, Username: "u", AuthType: "password", Password: "pw"})
	if !errors.Is(err, ErrKeychainUnavailable) {
		t.Fatalf("SaveSSHProfile error = %v, want ErrKeychainUnavailable", err)
	}
	// The host itself is saved, the password is not.
	got, err := s.GetSSHProfile("x")
	must(t, err)
	if got.Host != "h" || got.HasPassword {
		t.Fatalf("unexpected profile %+v", got)
	}
	var dbPassword string
	must(t, s.db.QueryRow(`SELECT password FROM ssh_profiles WHERE id='x'`).Scan(&dbPassword))
	if dbPassword != "" {
		t.Fatalf("plaintext password written to the database")
	}

	err = s.SaveRDPProfile(rdp.RDPProfile{ID: "r", Name: "r", Host: "h", Port: 3389, Password: "pw"})
	if !errors.Is(err, ErrKeychainUnavailable) {
		t.Fatalf("SaveRDPProfile error = %v, want ErrKeychainUnavailable", err)
	}
	must(t, s.db.QueryRow(`SELECT password FROM rdp_profiles WHERE id='r'`).Scan(&dbPassword))
	if dbPassword != "" {
		t.Fatalf("plaintext RDP password written to the database")
	}
}

func TestLegacyPlaintextPasswordStillReadable(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"), &failingSecrets{NewMemorySecrets()})
	must(t, err)
	defer s.Close()
	must(t, s.SaveSSHProfile(SSHProfile{ID: "x", Name: "x", Host: "h", Port: 22, Username: "u", AuthType: "password"}))
	_, err = s.db.Exec(`UPDATE ssh_profiles SET password='old' WHERE id='x'`)
	must(t, err)
	if pw, _ := s.SSHPassword("x"); pw != "old" {
		t.Fatalf("legacy password = %q", pw)
	}
}

func TestDatabaseFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	path := filepath.Join(t.TempDir(), "t.db")
	must(t, os.WriteFile(path, nil, 0o644))
	s, err := Open(path, NewMemorySecrets())
	must(t, err)
	defer s.Close()
	fi, err := os.Stat(path)
	must(t, err)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestPasswordKeepUpdateClear(t *testing.T) {
	s, sec, _ := openTest(t)
	p := SSHProfile{ID: "x", Name: "x", Host: "h", Port: 22, Username: "u", AuthType: "password", Password: "one"}
	must(t, s.SaveSSHProfile(p))

	// Editing without a password keeps the stored one.
	p.Password = ""
	p.Name = "renamed"
	must(t, s.SaveSSHProfile(p))
	if pw, _ := s.SSHPassword("x"); pw != "one" {
		t.Fatalf("password lost on edit: %q", pw)
	}
	got, _ := s.GetSSHProfile("x")
	if got.Name != "renamed" || !got.HasPassword || got.Password != "" {
		t.Fatalf("unexpected profile %+v", got)
	}

	p.Password = "two"
	must(t, s.SaveSSHProfile(p))
	if pw, _ := s.SSHPassword("x"); pw != "two" {
		t.Fatalf("password not updated: %q", pw)
	}

	p.Password = ""
	p.ClearPassword = true
	must(t, s.SaveSSHProfile(p))
	if pw, _ := s.SSHPassword("x"); pw != "" {
		t.Fatalf("password not cleared: %q", pw)
	}

	must(t, s.SaveSSHProfile(SSHProfile{ID: "y", Name: "y", Host: "h", Port: 22, Username: "u", Password: "z"}))
	must(t, s.DeleteSSHProfile("y"))
	if _, err := sec.Get("ssh:y"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatal("secret not removed with profile")
	}
}

func ids[T any](items []T, id func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = id(it)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFoldersMoveReorderDelete(t *testing.T) {
	s, _, _ := openTest(t)
	must(t, s.SaveFolder(Folder{ID: "prod", Name: "Prod"}))
	must(t, s.SaveFolder(Folder{ID: "db", Name: "Databases", ParentID: "prod"}))
	must(t, s.SaveFolder(Folder{ID: "dev", Name: "Dev"}))
	if err := s.SaveFolder(Folder{ID: "bad", Name: "x", ParentID: "missing"}); err == nil {
		t.Fatal("folder with missing parent accepted")
	}

	for _, id := range []string{"h1", "h2", "h3"} {
		must(t, s.SaveSSHProfile(SSHProfile{ID: id, Name: id, Host: id, Port: 22, Username: "u", FolderID: "prod"}))
	}
	must(t, s.SaveRDPProfile(rdp.RDPProfile{ID: "r1", Name: "r1", Host: "r", Port: 3389, FolderID: "prod"}))

	hostsIn := func(folder string) []string {
		ssh, _ := s.ListSSHProfiles()
		rd, _ := s.ListRDPProfiles()
		type h struct {
			id    string
			order int
		}
		var all []h
		for _, p := range ssh {
			if p.FolderID == folder {
				all = append(all, h{p.ID, p.SortOrder})
			}
		}
		for _, p := range rd {
			if p.FolderID == folder {
				all = append(all, h{p.ID, p.SortOrder})
			}
		}
		for i := range all {
			for j := i + 1; j < len(all); j++ {
				if all[j].order < all[i].order {
					all[i], all[j] = all[j], all[i]
				}
			}
		}
		return ids(all, func(x h) string { return x.id })
	}

	if got := hostsIn("prod"); !eq(got, []string{"h1", "h2", "h3", "r1"}) {
		t.Fatalf("append order: %v", got)
	}
	// Reorder within folder: RDP host to the front.
	must(t, s.MoveItem(KindRDP, "r1", "prod", 0))
	if got := hostsIn("prod"); !eq(got, []string{"r1", "h1", "h2", "h3"}) {
		t.Fatalf("reorder: %v", got)
	}
	// Move into another folder at position 0, and to root (append).
	must(t, s.MoveItem(KindSSH, "h2", "db", 0))
	must(t, s.MoveItem(KindSSH, "h3", "", -1))
	if got := hostsIn("prod"); !eq(got, []string{"r1", "h1"}) {
		t.Fatalf("after moves prod: %v", got)
	}
	if got := hostsIn("db"); !eq(got, []string{"h2"}) {
		t.Fatalf("after moves db: %v", got)
	}

	// Folder cycles are rejected.
	if err := s.MoveItem(KindFolder, "prod", "db", 0); !errors.Is(err, ErrFolderCycle) {
		t.Fatalf("cycle not detected: %v", err)
	}
	if err := s.MoveItem(KindFolder, "prod", "prod", 0); !errors.Is(err, ErrFolderCycle) {
		t.Fatalf("self-parent not detected: %v", err)
	}
	must(t, s.MoveItem(KindFolder, "dev", "", 0))
	folders, _ := s.ListFolders()
	var roots []string
	for _, f := range folders {
		if f.ParentID == "" {
			roots = append(roots, f.ID)
		}
	}
	if !eq(roots, []string{"dev", "prod"}) {
		t.Fatalf("folder reorder: %v", roots)
	}

	// Deleting a folder moves its contents to the parent.
	must(t, s.DeleteFolder("db"))
	if got := hostsIn("prod"); len(got) != 3 {
		t.Fatalf("hosts not reparented: %v", got)
	}
	must(t, s.DeleteFolder("prod"))
	if got := hostsIn(""); len(got) != 4 {
		t.Fatalf("hosts not moved to root: %v", got)
	}
	folders, _ = s.ListFolders()
	if len(folders) != 1 || folders[0].ID != "dev" {
		t.Fatalf("folders left: %+v", folders)
	}

	// Rename keeps parent.
	must(t, s.SaveFolder(Folder{ID: "dev", Name: "Development", Color: "#ff0"}))
	folders, _ = s.ListFolders()
	if folders[0].Name != "Development" || folders[0].Color != "#ff0" {
		t.Fatalf("rename failed: %+v", folders[0])
	}
}

func TestSettings(t *testing.T) {
	s, _, _ := openTest(t)
	v, err := s.GetSetting("app")
	must(t, err)
	if v != "" {
		t.Fatalf("unset setting = %q, want empty", v)
	}
	must(t, s.SetSetting("app", `{"a":1}`))
	must(t, s.SetSetting("app", `{"a":2}`))
	v, err = s.GetSetting("app")
	must(t, err)
	if v != `{"a":2}` {
		t.Fatalf("setting = %q", v)
	}
}

func TestRDPOptionsAndPassword(t *testing.T) {
	sec := NewMemorySecrets()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"), sec)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	opts := rdp.DefaultOptions()
	opts.Printers = true
	opts.DrivePaths = []string{"/a"}
	must(t, s.SaveRDPProfile(rdp.RDPProfile{ID: "x", Name: "x", Host: "h", Password: "pw", Options: opts}))
	l, _ := s.ListRDPProfiles()
	if len(l) != 1 || l[0].Password != "" || !l[0].HasPassword || !l[0].Options.Printers || len(l[0].Options.DrivePaths) != 1 {
		t.Fatalf("list: %+v", l)
	}
	// Saving with an empty password keeps the stored one.
	must(t, s.SaveRDPProfile(rdp.RDPProfile{ID: "x", Name: "x", Host: "h", Options: opts}))
	g, _ := s.GetRDPProfile("x")
	if g.Password != "pw" {
		t.Fatalf("password lost: %+v", g)
	}
	must(t, s.SaveRDPProfile(rdp.RDPProfile{ID: "x", Name: "x", Host: "h", ClearPassword: true, Options: opts}))
	if g, _ = s.GetRDPProfile("x"); g.Password != "" || g.HasPassword {
		t.Fatalf("password not cleared: %+v", g)
	}
}

func TestSSHJumpAndTunnelsRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"), NewMemorySecrets())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	must(t, s.SaveSSHProfile(SSHProfile{ID: "a", Name: "a", Host: "h", Username: "u", JumpProfileID: "a",
		Tunnels: []Tunnel{{ID: "t1", Kind: "local", BindPort: 8080, DestHost: "db", DestPort: 5432, AutoStart: true}}}))
	p, err := s.GetSSHProfile("a")
	if err != nil {
		t.Fatal(err)
	}
	if p.JumpProfileID != "" {
		t.Error("a host must not jump through itself")
	}
	if len(p.Tunnels) != 1 || !p.Tunnels[0].AutoStart || p.Tunnels[0].DestPort != 5432 {
		t.Fatalf("tunnels: %+v", p.Tunnels)
	}
}

func TestSSHSFTPOnlyRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"), NewMemorySecrets())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	must(t, s.SaveSSHProfile(SSHProfile{ID: "a", Name: "a", Host: "h", Username: "u", SFTPOnly: true}))
	must(t, s.SaveSSHProfile(SSHProfile{ID: "b", Name: "b", Host: "h", Username: "u"}))
	a, _ := s.GetSSHProfile("a")
	b, _ := s.GetSSHProfile("b")
	if !a.SFTPOnly || b.SFTPOnly {
		t.Fatalf("sftpOnly: a=%v b=%v", a.SFTPOnly, b.SFTPOnly)
	}
	a.SFTPOnly = false
	must(t, s.SaveSSHProfile(a))
	if a, _ = s.GetSSHProfile("a"); a.SFTPOnly {
		t.Fatal("sftpOnly should clear on update")
	}
}

func TestSnippets(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"), NewMemorySecrets())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	must(t, s.SaveSnippet(Snippet{ID: "1", Name: "Disk", Command: "df -h {{path:/}}", Group: "Ops", Execute: true}))
	if s.SaveSnippet(Snippet{ID: "2", Name: "", Command: "x"}) == nil {
		t.Error("empty name must be rejected")
	}
	must(t, s.SaveSnippet(Snippet{ID: "1", Name: "Disk usage", Command: "df -h", Group: "Ops"}))
	l, _ := s.ListSnippets()
	if len(l) != 1 || l[0].Name != "Disk usage" || l[0].Execute {
		t.Fatalf("%+v", l)
	}
	must(t, s.DeleteSnippet("1"))
	if l, _ = s.ListSnippets(); len(l) != 0 {
		t.Fatal("not deleted")
	}
}
