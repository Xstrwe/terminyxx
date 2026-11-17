package store

import (
	"errors"
	"fmt"

	"terminyxx/internal/rdp"
)

// BundleVersion is bumped when the exported shape changes incompatibly.
const BundleVersion = 1

// Bundle is everything that moves between machines: hosts, folders and
// snippets. App settings and the lock password stay local.
type Bundle struct {
	Version  int              `json:"version"`
	Folders  []Folder         `json:"folders"`
	SSH      []SSHProfile     `json:"ssh"`
	RDP      []rdp.RDPProfile `json:"rdp"`
	Snippets []Snippet        `json:"snippets"`
}

// ImportResult counts what an import added or replaced.
type ImportResult struct {
	Folders  int `json:"folders"`
	Hosts    int `json:"hosts"`
	Snippets int `json:"snippets"`
	// PasswordsNotSaved counts hosts imported without their password
	// because the OS keychain was unavailable.
	PasswordsNotSaved int `json:"passwordsNotSaved"`
}

// ExportBundle collects the data to sync. Saved passwords are included only
// if asked; the bundle is encrypted as a whole before it leaves the app.
func (s *Store) ExportBundle(includePasswords bool) (Bundle, error) {
	b := Bundle{Version: BundleVersion}
	var err error
	if b.Folders, err = s.ListFolders(); err != nil {
		return b, err
	}
	ssh, err := s.ListSSHProfiles()
	if err != nil {
		return b, err
	}
	for _, p := range ssh {
		p.Password, p.HasPassword, p.ClearPassword = "", false, false
		if includePasswords {
			pw, err := s.SSHPassword(p.ID)
			if err != nil {
				return b, err
			}
			p.Password = pw
		}
		b.SSH = append(b.SSH, p)
	}
	list, err := s.ListRDPProfiles()
	if err != nil {
		return b, err
	}
	for _, p := range list {
		p.Password, p.HasPassword, p.ClearPassword = "", false, false
		if includePasswords {
			full, err := s.GetRDPProfile(p.ID)
			if err != nil {
				return b, err
			}
			p.Password = full.Password
		}
		b.RDP = append(b.RDP, p)
	}
	if b.Snippets, err = s.ListSnippets(); err != nil {
		return b, err
	}
	return b, nil
}

// ImportBundle merges a bundle into the database by ID: items in the bundle
// replace local ones with the same ID, everything else is kept. Nothing is
// ever deleted, so a host removed on another machine stays here.
func (s *Store) ImportBundle(b Bundle) (ImportResult, error) {
	var res ImportResult
	if b.Version < 1 || b.Version > BundleVersion {
		return res, fmt.Errorf("unsupported sync file version %d", b.Version)
	}

	// Parents first, so every SaveFolder finds its parent.
	pending := append([]Folder(nil), b.Folders...)
	for len(pending) > 0 {
		var rest []Folder
		for _, f := range pending {
			if f.ParentID != "" && !s.folderExists(f.ParentID) {
				rest = append(rest, f)
				continue
			}
			if err := s.SaveFolder(f); err != nil {
				return res, err
			}
			res.Folders++
		}
		if len(rest) == len(pending) {
			// Orphans (parent missing from the file) go to the top level.
			for _, f := range rest {
				f.ParentID = ""
				if err := s.SaveFolder(f); err != nil {
					return res, err
				}
				res.Folders++
			}
			break
		}
		pending = rest
	}

	for _, p := range b.SSH {
		if p.FolderID != "" && !s.folderExists(p.FolderID) {
			p.FolderID = ""
		}
		if err := s.SaveSSHProfile(p); errors.Is(err, ErrKeychainUnavailable) {
			res.PasswordsNotSaved++
		} else if err != nil {
			return res, err
		}
		res.Hosts++
	}
	for _, p := range b.RDP {
		if p.FolderID != "" && !s.folderExists(p.FolderID) {
			p.FolderID = ""
		}
		if err := s.SaveRDPProfile(p); errors.Is(err, ErrKeychainUnavailable) {
			res.PasswordsNotSaved++
		} else if err != nil {
			return res, err
		}
		res.Hosts++
	}
	for _, sn := range b.Snippets {
		if err := s.SaveSnippet(sn); err != nil {
			return res, err
		}
		res.Snippets++
	}
	return res, nil
}
