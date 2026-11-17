package recording

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordAndPlayback(t *testing.T) {
	dir := t.TempDir()
	r, name, err := Start(dir, "root@host:22", 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(name, ".cast") || strings.ContainsAny(name, `/\:@`) {
		t.Fatalf("unsafe name %q", name)
	}
	r.Output("hello \x1b[31mwor")
	r.Output("ld\x1b[0m\r\nsecond line\r\n")
	r.Resize(80, 24)
	r.Close()
	r.Output("after close") // ignored

	rec, err := Read(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Info.Cols != 100 || len(rec.Events) != 3 || rec.Events[2].Kind != "r" || rec.Events[2].Data != "80x24" {
		t.Fatalf("%+v", rec)
	}
	list, _ := List(dir)
	if len(list) != 1 || list[0].Title != "root@host:22" {
		t.Fatalf("list %+v", list)
	}
	// List reads only the header and the tail but reports the same info.
	if list[0] != rec.Info {
		t.Fatalf("list info %+v, read info %+v", list[0], rec.Info)
	}

	// The line is split across two chunks and has colour codes: still found.
	hits, _ := Search(dir, "HELLO world", 10)
	if len(hits) != 1 || hits[0].Line != "hello world" {
		t.Fatalf("hits %+v", hits)
	}
	if hits, _ = Search(dir, "nothing", 10); len(hits) != 0 {
		t.Fatalf("unexpected %+v", hits)
	}
}

func TestNameValidation(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "..", "x.cast")
	_ = os.WriteFile(secret, []byte("x"), 0o600)
	defer os.Remove(secret)
	for _, bad := range []string{"../x.cast", "a/b.cast", `a\b.cast`, "x.txt", "", ".hidden.cast"} {
		if _, err := Read(dir, bad); err == nil {
			t.Errorf("Read accepted %q", bad)
		}
		if err := Delete(dir, bad); err == nil {
			t.Errorf("Delete accepted %q", bad)
		}
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatal("file outside the directory was touched")
	}
}

func TestTornLastLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.cast")
	os.WriteFile(p, []byte("{\"version\":2,\"width\":80,\"height\":24,\"timestamp\":1}\n[0.5,\"o\",\"ok\"]\n[0.9,\"o\",\"tor"), 0o600)
	rec, err := Read(dir, "a.cast")
	if err != nil || len(rec.Events) != 1 {
		t.Fatalf("%+v %v", rec, err)
	}
}
