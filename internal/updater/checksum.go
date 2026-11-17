package updater

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// ParseChecksums reads a SHA256SUMS-style file (the format sha256sum writes:
// "<hex>  <name>" or "<hex> *<name>") and returns a name-to-hex map.
func ParseChecksums(raw []byte) (map[string]string, error) {
	out := make(map[string]string)
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		hexSum, name, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("malformed checksum line %q", line)
		}
		name = strings.TrimSpace(name)
		name = strings.TrimPrefix(name, "*")
		if name == "" || len(hexSum) != sha256.Size*2 {
			return nil, fmt.Errorf("malformed checksum line %q", line)
		}
		if _, err := hex.DecodeString(hexSum); err != nil {
			return nil, fmt.Errorf("malformed checksum line %q", line)
		}
		out[name] = strings.ToLower(hexSum)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("checksum file is empty")
	}
	return out, nil
}

// ChecksumFor returns the expected hex digest for name, or an error if missing.
func ChecksumFor(sums map[string]string, name string) (string, error) {
	if sums == nil {
		return "", fmt.Errorf("checksum for %s is missing", name)
	}
	sum, ok := sums[name]
	if !ok || sum == "" {
		return "", fmt.Errorf("checksum for %s is missing", name)
	}
	return sum, nil
}

// VerifySHA256 checks that the file at path has the expected SHA-256 hex digest.
func VerifySHA256(path, wantHex string) error {
	want, err := hex.DecodeString(strings.TrimSpace(wantHex))
	if err != nil || len(want) != sha256.Size {
		return fmt.Errorf("invalid checksum")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := h.Sum(nil)
	if !bytes.Equal(got, want) {
		return fmt.Errorf("checksum mismatch: got %s, want %s", hex.EncodeToString(got), strings.ToLower(wantHex))
	}
	return nil
}
