package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestLocalNetworkHint(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)}
	wrapped := fmt.Errorf("could not reach 192.168.31.70: %w", dial)

	got := localNetworkHintFor(wrapped, "darwin")
	if !strings.Contains(got.Error(), "Local Network") || !errors.Is(got, syscall.EHOSTUNREACH) {
		t.Fatalf("darwin: %v", got)
	}
	// Prefixes the frontend parses must stay at the start.
	if !strings.HasPrefix(got.Error(), "could not reach") {
		t.Fatalf("hint must be appended: %v", got)
	}
	// A plain string (e.g. flattened by %v) is recognized too.
	if s := localNetworkHintFor(errors.New("dial tcp: connect: no route to host"), "darwin"); !strings.Contains(s.Error(), "Local Network") {
		t.Fatalf("string match: %v", s)
	}
	if got := localNetworkHintFor(wrapped, "windows"); got != wrapped {
		t.Fatalf("other OS must be unchanged: %v", got)
	}
	other := errors.New("connection refused")
	if got := localNetworkHintFor(other, "darwin"); got != other {
		t.Fatalf("unrelated error changed: %v", got)
	}
}
