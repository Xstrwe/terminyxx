// Package recording saves terminal sessions as asciicast v2 files
// (https://docs.asciinema.org/manual/asciicast/v2/) and manages the library.
// Only what the terminal displayed is recorded, never keyboard input, so
// typed passwords do not end up on disk.
package recording

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const ext = ".cast"

// Recorder appends events to one .cast file. It is safe for concurrent use.
type Recorder struct {
	mu     sync.Mutex
	f      *os.File
	w      *bufio.Writer
	start  time.Time
	closed bool
	bytes  int64
}

// maxBytes stops a runaway session (e.g. `cat /dev/urandom`) from filling the disk.
const maxBytes = 256 << 20

type header struct {
	Version   int               `json:"version"`
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	Timestamp int64             `json:"timestamp"`
	Title     string            `json:"title,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Start creates a new recording in dir and returns the recorder and file name.
func Start(dir, title string, cols, rows int) (*Recorder, string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	now := time.Now()
	name := fmt.Sprintf("%s-%s%s", now.Format("20060102-150405"), strings.Trim(unsafeName.ReplaceAllString(title, "_"), "_"), ext)
	if name == now.Format("20060102-150405")+"-"+ext {
		name = now.Format("20060102-150405") + ext
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", err
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 30
	}
	r := &Recorder{f: f, w: bufio.NewWriter(f), start: now}
	h, _ := json.Marshal(header{Version: 2, Width: cols, Height: rows, Timestamp: now.Unix(), Title: title, Env: map[string]string{"TERM": "xterm-256color"}})
	r.w.Write(h)
	r.w.WriteByte('\n')
	_ = r.w.Flush()
	return r, name, nil
}

func (r *Recorder) event(kind, data string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	line, _ := json.Marshal([]any{time.Since(r.start).Seconds(), kind, data})
	r.bytes += int64(len(line)) + 1
	if r.bytes > maxBytes {
		r.closeLocked()
		return
	}
	r.w.Write(line)
	r.w.WriteByte('\n')
	// Flush each event so a crash loses at most the last line.
	_ = r.w.Flush()
}

// Output records terminal output.
func (r *Recorder) Output(s string) { r.event("o", s) }

// Resize records a terminal size change.
func (r *Recorder) Resize(cols, rows int) { r.event("r", fmt.Sprintf("%dx%d", cols, rows)) }

func (r *Recorder) closeLocked() {
	if r.closed {
		return
	}
	r.closed = true
	_ = r.w.Flush()
	_ = r.f.Close()
}

// Close finishes the file. Further events are ignored.
func (r *Recorder) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeLocked()
}

// Info describes a recording in the library.
type Info struct {
	Name      string  `json:"name"`
	Title     string  `json:"title"`
	StartedAt int64   `json:"startedAt"`
	Duration  float64 `json:"duration"`
	Size      int64   `json:"size"`
	Cols      int     `json:"cols"`
	Rows      int     `json:"rows"`
}

// Event is one line of a recording.
type Event struct {
	Time float64 `json:"time"`
	Kind string  `json:"kind"`
	Data string  `json:"data"`
}

// Recording is a whole file, ready for playback.
type Recording struct {
	Info   Info    `json:"info"`
	Events []Event `json:"events"`
}

// validName accepts only plain file names that are .cast files. A backslash
// is rejected on every OS: it separates paths on Windows and recordings never
// contain one.
func validName(name string) bool {
	return name != "" && name == filepath.Base(name) && !strings.ContainsAny(name, `/\`) && strings.HasSuffix(name, ext) && !strings.HasPrefix(name, ".")
}

// openRecording opens path and parses its header line. The returned scanner
// is positioned at the first event.
func openRecording(path string) (*os.File, *bufio.Scanner, Info, error) {
	var info Info
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, info, err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	if !sc.Scan() {
		f.Close()
		return nil, nil, info, errors.New("empty recording")
	}
	var h header
	if err := json.Unmarshal(sc.Bytes(), &h); err != nil || h.Version != 2 {
		f.Close()
		return nil, nil, info, errors.New("not an asciicast v2 file")
	}
	info = Info{Name: filepath.Base(path), Title: h.Title, StartedAt: h.Timestamp, Cols: h.Width, Rows: h.Height}
	if st, err := f.Stat(); err == nil {
		info.Size = st.Size()
	}
	return f, sc, info, nil
}

// parseEvent decodes one event line; ok is false for a malformed (e.g.
// half-written) line.
func parseEvent(line []byte) (e Event, ok bool) {
	var raw []json.RawMessage
	if json.Unmarshal(line, &raw) != nil || len(raw) != 3 {
		return e, false
	}
	if json.Unmarshal(raw[0], &e.Time) != nil || json.Unmarshal(raw[1], &e.Kind) != nil || json.Unmarshal(raw[2], &e.Data) != nil {
		return e, false
	}
	return e, true
}

// eachEvent streams the events of a recording to fn without keeping them in
// memory; fn returns false to stop early.
func eachEvent(path string, fn func(Info, Event) bool) (Info, error) {
	f, sc, info, err := openRecording(path)
	if err != nil {
		return info, err
	}
	defer f.Close()
	for sc.Scan() {
		e, ok := parseEvent(sc.Bytes())
		if !ok {
			continue // a half-written last line
		}
		info.Duration = e.Time
		if !fn(info, e) {
			break
		}
	}
	return info, sc.Err()
}

func readFile(path string) (Recording, error) {
	rec := Recording{Events: []Event{}}
	info, err := eachEvent(path, func(_ Info, e Event) bool {
		rec.Events = append(rec.Events, e)
		return true
	})
	rec.Info = info
	return rec, err
}

// tailWindow is how much of the end of a file List reads to find the
// duration (the time of the last complete event).
const tailWindow = 64 << 10

// readInfo reads only the header line and the tail of a recording, so
// listing the library does not parse (up to 256 MB) files completely.
func readInfo(path string) (Info, error) {
	f, _, info, err := openRecording(path)
	if err != nil {
		return info, err
	}
	defer f.Close()
	off := info.Size - tailWindow
	if off < 0 {
		off = 0
	}
	buf := make([]byte, info.Size-off)
	n, err := f.ReadAt(buf, off)
	if err != nil && n == 0 {
		return info, nil
	}
	lines := strings.Split(string(buf[:n]), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if e, ok := parseEvent([]byte(lines[i])); ok {
			info.Duration = e.Time
			break
		}
	}
	return info, nil
}

// List returns the recordings in dir, newest first.
func List(dir string) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Info{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Info{}
	for _, e := range entries {
		if e.IsDir() || !validName(e.Name()) {
			continue
		}
		info, err := readInfo(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out, nil
}

// Read loads one recording for playback.
func Read(dir, name string) (Recording, error) {
	if !validName(name) {
		return Recording{}, errors.New("invalid recording name")
	}
	return readFile(filepath.Join(dir, name))
}

// Delete removes one recording.
func Delete(dir, name string) error {
	if !validName(name) {
		return errors.New("invalid recording name")
	}
	err := os.Remove(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ansi matches escape sequences so search sees only the visible text.
var ansi = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[@-Z\\\\-_]")

// Hit is a search result: a line of output containing the query.
type Hit struct {
	Name    string  `json:"name"`
	Title   string  `json:"title"`
	Time    float64 `json:"time"`
	Line    string  `json:"line"`
	Started int64   `json:"startedAt"`
}

// maxLine caps how much of one output line Search keeps for matching.
const maxLine = 16 << 10

// Search finds lines containing query (case-insensitive) in all recordings.
// Output is joined across events, so a line split over several chunks still
// matches. At most limit hits are returned.
func Search(dir, query string, limit int) ([]Hit, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return []Hit{}, nil
	}
	infos, err := List(dir)
	if err != nil {
		return nil, err
	}
	hits := []Hit{}
	for _, info := range infos {
		var cur strings.Builder
		var lineStart float64
		flush := func() {
			line := strings.TrimSpace(cur.String())
			cur.Reset()
			if line != "" && strings.Contains(strings.ToLower(line), q) && len(hits) < limit {
				if len(line) > 300 {
					line = line[:300]
				}
				hits = append(hits, Hit{Name: info.Name, Title: info.Title, Time: lineStart, Line: line, Started: info.StartedAt})
			}
		}
		// Stream the file: a recording can be up to 256 MB.
		_, err := eachEvent(filepath.Join(dir, info.Name), func(_ Info, e Event) bool {
			if e.Kind != "o" {
				return true
			}
			text := ansi.ReplaceAllString(e.Data, "")
			for _, ch := range text {
				switch ch {
				case '\n':
					flush()
				case '\r':
				default:
					if cur.Len() == 0 {
						lineStart = e.Time
					}
					// Bound a line that never ends (no newline in the output).
					if (ch >= ' ' || ch == '\t') && cur.Len() < maxLine {
						cur.WriteRune(ch)
					}
				}
			}
			return len(hits) < limit
		})
		if err != nil {
			continue
		}
		flush()
		if len(hits) >= limit {
			break
		}
	}
	return hits, nil
}
