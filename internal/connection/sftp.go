package connection

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const maxPreviewBytes = 512 * 1024

var ErrBinaryFile = errors.New("binary file cannot be previewed")

type SFTPBridge struct {
	client *sftp.Client
}

func NewSFTPBridge(sshClient *ssh.Client) (*SFTPBridge, error) {
	client, err := sftp.NewClient(sshClient,
		sftp.UseConcurrentWrites(true),
		sftp.UseConcurrentReads(true),
	)
	if err != nil {
		return nil, fmt.Errorf("create sftp client: %w", err)
	}
	return &SFTPBridge{client: client}, nil
}

func (b *SFTPBridge) Close() error {
	return b.client.Close()
}

func sortEntries(entries []FileEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
}

func toEntry(name, fullPath string, info os.FileInfo) FileEntry {
	return FileEntry{
		Name:    name,
		Path:    fullPath,
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		ModTime: info.ModTime().Format(time.RFC3339),
	}
}

// Limits for listing a remote folder: the server decides how many entries
// it sends, so stop reading after listDirTimeout, show at most maxDirEntries
// and resolve at most maxSymlinkStats symlinks (one round trip each).
const (
	listDirTimeout  = 30 * time.Second
	maxDirEntries   = 100_000
	maxSymlinkStats = 2_000
)

func (b *SFTPBridge) ListDir(dirPath string) ([]FileEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), listDirTimeout)
	defer cancel()
	infos, err := b.client.ReadDirContext(ctx, dirPath)
	if err != nil && !(errors.Is(err, context.DeadlineExceeded) && len(infos) > 0) {
		return nil, err
	}
	if len(infos) > maxDirEntries {
		infos = infos[:maxDirEntries]
	}
	result := make([]FileEntry, 0, len(infos))
	stats := 0
	for _, info := range infos {
		full := path.Join(dirPath, info.Name())
		entry := toEntry(info.Name(), full, info)
		if info.Mode()&os.ModeSymlink != 0 {
			entry.IsLink = true
		}
		if entry.IsLink && stats < maxSymlinkStats {
			stats++
			if target, err := b.client.Stat(full); err == nil {
				entry.IsDir = target.IsDir()
				if !target.IsDir() {
					entry.Size = target.Size()
				}
			}
		}
		result = append(result, entry)
	}
	sortEntries(result)
	return result, nil
}

func (b *SFTPBridge) Mkdir(dirPath string) error {
	return b.client.MkdirAll(dirPath)
}

func (b *SFTPBridge) Remove(p string) error {
	info, err := b.client.Lstat(p)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return b.client.RemoveAll(p)
	}
	return b.client.Remove(p)
}

func (b *SFTPBridge) Rename(oldPath, newPath string) error {
	// PosixRename overwrites the target like `mv`; fall back if unsupported.
	if err := b.client.PosixRename(oldPath, newPath); err == nil {
		return nil
	}
	return b.client.Rename(oldPath, newPath)
}

func (b *SFTPBridge) ReadFile(filePath string) (string, error) {
	f, err := b.client.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return readPreview(f)
}

func (b *SFTPBridge) Getwd() (string, error) {
	return b.client.Getwd()
}

func readPreview(r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxPreviewBytes))
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", ErrBinaryFile
	}
	return string(data), nil
}
