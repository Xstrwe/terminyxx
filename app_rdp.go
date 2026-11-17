package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	"terminyxx/internal/connection"
	"terminyxx/internal/rdpgw"
)

// RDPSession is what the built-in RDP client (IronRDP in the webview) needs
// to start a session through the local gateway.
type RDPSession struct {
	ProxyURL    string `json:"proxyUrl"`
	Token       string `json:"token"`
	Destination string `json:"destination"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Domain      string `json:"domain"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Clipboard   bool   `json:"clipboard"`
	// Audio is the profile's audio mode: "local" plays remote sound here,
	// "remote" leaves it on the server, "off" mutes it.
	Audio string `json:"audio"`
}

// RDPOpen prepares a built-in RDP session for tabID. It fetches the server
// certificate first: an untrusted one comes back as an
// RDPCERT_UNKNOWN|… / RDPCERT_CHANGED|… error, and the retry passes its
// fingerprint as acceptFingerprint to trust it. password overrides the saved
// one (empty = use the keychain). The returned password exists only because
// CredSSP runs in the webview.
func (a *App) RDPOpen(tabID, profileID, password, acceptFingerprint string) (RDPSession, error) {
	if a.data == nil || a.rdpgw == nil {
		return RDPSession{}, errors.New("storage is unavailable")
	}
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return RDPSession{}, connection.ErrLocked
	}
	p, err := a.data.GetRDPProfile(profileID)
	if err != nil {
		return RDPSession{}, err
	}
	port := p.Port
	if port == 0 {
		port = 3389
	}
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	chain, err := rdpgw.Probe(ctx, p.Host, port)
	if err != nil {
		return RDPSession{}, localNetworkHint(fmt.Errorf("could not reach %s: %w", p.Host, err))
	}
	dest := net.JoinHostPort(p.Host, strconv.Itoa(port))
	if err := a.rdpgw.Trust().Check(dest, p.Host, chain); err != nil {
		var ue *rdpgw.UntrustedError
		if !errors.As(err, &ue) || acceptFingerprint == "" || acceptFingerprint != ue.Fingerprint {
			return RDPSession{}, err
		}
		if err := a.rdpgw.Trust().Trust(dest, ue.Fingerprint); err != nil {
			return RDPSession{}, fmt.Errorf("save certificate: %w", err)
		}
	}
	url, token, err := a.rdpgw.Issue(tabID, p.Host, port)
	if err != nil {
		return RDPSession{}, err
	}
	if password == "" {
		password = p.Password
	}
	return RDPSession{
		ProxyURL:    url,
		Token:       token,
		Destination: dest,
		Username:    p.Username,
		Password:    password,
		Domain:      p.Domain,
		Width:       p.Width,
		Height:      p.Height,
		Clipboard:   p.Options.Clipboard,
		Audio:       p.Options.Audio,
	}, nil
}

// RDPClose ends the built-in RDP session of tabID.
func (a *App) RDPClose(tabID string) {
	if a.rdpgw != nil {
		a.rdpgw.Close(tabID)
	}
}

// RDPLastError explains why the last session of tabID failed on the gateway
// side (the client itself only gets an error code).
func (a *App) RDPLastError(tabID string) string {
	if a.rdpgw == nil {
		return ""
	}
	return a.rdpgw.LastError(tabID)
}
