package connection

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"
)

const progressInterval = 100 * time.Millisecond

type transferItem struct {
	src   string
	dst   string
	size  int64
	isDir bool
	mode  os.FileMode
	mtime time.Time
}

type progressTracker struct {
	mu       sync.Mutex
	p        TransferProgress
	emitter  EventEmitter
	lastEmit time.Time
}

func (t *progressTracker) add(n int64) {
	t.mu.Lock()
	t.p.BytesDone += int64(n)
	due := time.Since(t.lastEmit) >= progressInterval
	t.mu.Unlock()
	if due {
		t.emit()
	}
}

func (t *progressTracker) update(fn func(p *TransferProgress)) {
	t.mu.Lock()
	fn(&t.p)
	t.mu.Unlock()
	t.emit()
}

func (t *progressTracker) emit() {
	t.mu.Lock()
	t.lastEmit = time.Now()
	p := t.p
	t.mu.Unlock()
	if t.emitter != nil {
		t.emitter.Emit("sftp:progress:"+p.ConnectionID, p)
	}
}

type countingReader struct {
	ctx context.Context
	r   io.Reader
	t   *progressTracker
}

func (c *countingReader) Read(b []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.r.Read(b)
	c.t.add(int64(n))
	return n, err
}

type countingWriter struct {
	ctx context.Context
	w   io.Writer
	t   *progressTracker
}

func (c *countingWriter) Write(b []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.w.Write(b)
	c.t.add(int64(n))
	return n, err
}

// Transfer copies files and directories (recursively) in either direction,
// emitting throttled sftp:progress:{connID} events.
func (b *SFTPBridge) Transfer(ctx context.Context, req TransferRequest, connID string, emitter EventEmitter) (err error) {
	tracker := &progressTracker{
		emitter: emitter,
		p:       TransferProgress{JobID: req.JobID, ConnectionID: connID, Direction: req.Direction},
	}
	defer func() {
		tracker.update(func(p *TransferProgress) {
			p.Done = true
			if err != nil {
				p.Error = err.Error()
			}
		})
	}()

	var items []transferItem
	switch req.Direction {
	case "upload":
		items, err = b.planUpload(req)
	case "download":
		items, err = b.planDownload(req)
	default:
		return fmt.Errorf("unknown direction %q", req.Direction)
	}
	if err != nil {
		return err
	}

	tracker.update(func(p *TransferProgress) {
		for _, it := range items {
			if !it.isDir {
				p.FilesTotal++
				p.BytesTotal += it.size
			}
		}
	})

	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if it.isDir {
			if req.Direction == "upload" {
				err = b.client.MkdirAll(it.dst)
			} else {
				err = os.MkdirAll(it.dst, 0o755)
			}
			if err != nil {
				return fmt.Errorf("mkdir %s: %w", it.dst, err)
			}
			continue
		}

		tracker.update(func(p *TransferProgress) { p.CurrentFile = it.src })
		if req.Direction == "upload" {
			err = b.uploadFile(ctx, it, tracker)
		} else {
			err = b.downloadFile(ctx, it, tracker)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", it.src, err)
		}
		tracker.update(func(p *TransferProgress) { p.FilesDone++ })
	}

	if req.Move {
		for _, src := range req.Sources {
			if req.Direction == "upload" {
				err = os.RemoveAll(src)
			} else {
				err = b.Remove(src)
			}
			if err != nil {
				return fmt.Errorf("remove source %s: %w", src, err)
			}
		}
	}
	return nil
}

func (b *SFTPBridge) planUpload(req TransferRequest) ([]transferItem, error) {
	var items []transferItem
	for _, src := range req.Sources {
		src = filepath.Clean(src)
		root := path.Join(req.DestDir, filepath.Base(src))
		err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := os.Stat(p) // follow symlinks to files
			if err != nil {
				return err
			}
			if d.Type()&fs.ModeSymlink != 0 && info.IsDir() {
				return nil // don't follow directory symlinks
			}
			rel, err := filepath.Rel(src, p)
			if err != nil {
				return err
			}
			dst := root
			if rel != "." {
				dst = path.Join(root, filepath.ToSlash(rel))
			}
			items = append(items, transferItem{
				src: p, dst: dst, size: info.Size(), isDir: info.IsDir(),
				mode: info.Mode().Perm(), mtime: info.ModTime(),
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (b *SFTPBridge) planDownload(req TransferRequest) ([]transferItem, error) {
	var items []transferItem
	for _, src := range req.Sources {
		src = path.Clean(src)
		root := filepath.Join(req.DestDir, path.Base(src))
		walker := b.client.Walk(src)
		for walker.Step() {
			if err := walker.Err(); err != nil {
				return nil, err
			}
			p := walker.Path()
			info := walker.Stat()
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := b.client.Stat(p)
				if err != nil || target.IsDir() {
					continue // dangling or directory symlink
				}
				info = target
			}
			dst, err := downloadTarget(root, src, p)
			if err != nil {
				return nil, err
			}
			items = append(items, transferItem{
				src: p, dst: dst, size: info.Size(), isDir: info.IsDir(),
				mode: info.Mode().Perm(), mtime: info.ModTime(),
			})
		}
	}
	return items, nil
}

// downloadTarget maps remote path p (found while walking src) to a local
// path under root. Names come from the server, which could send "../x" or,
// for Windows, "..\x"; anything that would land outside root is refused.
func downloadTarget(root, src, p string) (string, error) {
	var rel string
	switch {
	case p == src:
		return root, nil
	case src == "/" && strings.HasPrefix(p, "/"):
		rel = p[1:]
	case strings.HasPrefix(p, src+"/"):
		rel = p[len(src)+1:]
	default:
		return "", fmt.Errorf("server returned a path outside %s: %q", src, p)
	}
	if goruntime.GOOS == "windows" {
		for _, part := range strings.Split(rel, "/") {
			if unsafeWindowsName(part) {
				return "", fmt.Errorf("refusing unsafe file name from server: %q", rel)
			}
		}
	}
	dst := filepath.Join(root, filepath.FromSlash(rel))
	r, err := filepath.Rel(root, dst)
	if err != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", fmt.Errorf("refusing unsafe file name from server: %q", rel)
	}
	return dst, nil
}

// windowsReserved are device names Windows resolves in any folder
// (with or without an extension): writing "NUL.txt" or "COM1" does not
// create a file.
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	"COM¹": true, "COM²": true, "COM³": true, "LPT¹": true, "LPT²": true, "LPT³": true,
}

// unsafeWindowsName reports whether a name from the server would be a
// device, an alternate data stream ("file:stream") or be silently renamed
// by Windows (trailing dot or space).
func unsafeWindowsName(name string) bool {
	if name == "" {
		return false
	}
	if strings.ContainsAny(name, `:<>"|?*`) || strings.ContainsFunc(name, func(r rune) bool { return r < 32 }) {
		return true
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return name != "." && name != ".."
	}
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	return windowsReserved[strings.ToUpper(strings.TrimRight(base, " "))]
}

// sensitiveFile reports whether a downloaded file looks like a key or
// credential, which is then saved readable by the current user only.
func sensitiveFile(remotePath string) bool {
	p := strings.ToLower(remotePath)
	if strings.Contains(p, "/.ssh/") || strings.Contains(p, "/.gnupg/") || strings.Contains(p, "/.aws/") {
		return true
	}
	name := path.Base(p)
	if strings.HasPrefix(name, "id_") && !strings.HasSuffix(name, ".pub") {
		return true
	}
	for _, ext := range []string{".pem", ".key", ".ppk", ".p12", ".pfx", ".kdbx", ".env"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return name == ".env" || name == ".netrc" || name == ".pgpass" || name == "credentials"
}

// downloadMode is the mode for a downloaded file: the remote permission
// bits (no setuid/sticky), 0600 for keys and credentials, 0644 when the
// server sent none. The process umask still applies on creation.
func downloadMode(it transferItem) os.FileMode {
	if sensitiveFile(it.src) {
		return 0o600
	}
	if m := it.mode.Perm(); m != 0 {
		return m | 0o600 // the user must be able to read and replace it
	}
	return 0o644
}

func (b *SFTPBridge) uploadFile(ctx context.Context, it transferItem, t *progressTracker) error {
	src, err := os.Open(it.src)
	if err != nil {
		return err
	}
	defer src.Close()

	if err := b.client.MkdirAll(path.Dir(it.dst)); err != nil {
		return err
	}
	dst, err := b.client.OpenFile(it.dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	_, err = dst.ReadFromWithConcurrency(&countingReader{ctx: ctx, r: src, t: t}, 0)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			_ = b.client.Remove(it.dst)
		}
		return err
	}
	_ = b.client.Chmod(it.dst, it.mode)
	_ = b.client.Chtimes(it.dst, it.mtime, it.mtime)
	return nil
}

func (b *SFTPBridge) downloadFile(ctx context.Context, it transferItem, t *progressTracker) error {
	src, err := b.client.Open(it.src)
	if err != nil {
		return err
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(it.dst), 0o755); err != nil {
		return err
	}
	mode := downloadMode(it)
	dst, err := os.OpenFile(it.dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if mode == 0o600 {
		// An existing file keeps its old mode on O_TRUNC; tighten it.
		_ = dst.Chmod(mode)
	}
	_, err = src.WriteTo(&countingWriter{ctx: ctx, w: dst, t: t})
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			_ = os.Remove(it.dst)
		}
		return err
	}
	_ = os.Chtimes(it.dst, it.mtime, it.mtime)
	return nil
}
