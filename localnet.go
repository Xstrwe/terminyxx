package main

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall"
)

// localNetworkHint explains EHOSTUNREACH on macOS. Since macOS 15 an app
// needs the Local Network permission to connect to addresses on the LAN;
// without it the connect fails with "no route to host" even though ping
// (from Terminal) works.
func localNetworkHint(err error) error {
	return localNetworkHintFor(err, runtime.GOOS)
}

func localNetworkHintFor(err error, goos string) error {
	if err == nil || goos != "darwin" {
		return err
	}
	if !errors.Is(err, syscall.EHOSTUNREACH) && !strings.Contains(err.Error(), "no route to host") {
		return err
	}
	return fmt.Errorf("%w. On macOS this usually means Terminyxx is not allowed to use the local network: "+
		"turn it on in System Settings → Privacy & Security → Local Network, then quit and reopen Terminyxx", err)
}
