package connection

import (
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"terminyxx/internal/recording"
)

// echoServer accepts connections and echoes with an "echo:" prefix.
func echoServer(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 64)
				n, _ := c.Read(buf)
				c.Write(append([]byte("echo:"), buf[:n]...))
			}()
		}
	}()
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	pn, _ := strconv.Atoi(p)
	return h, pn
}

func roundTrip(t *testing.T, addr, msg string) string {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write([]byte(msg))
	out, _ := io.ReadAll(c)
	return string(out)
}

func connected(t *testing.T) (*ConnectionManager, string) {
	t.Helper()
	srv := newTestServer(t, serverOpts{password: "secret"})
	m, _, _ := newManager(t)
	connectTrusted(t, m, passwordReq(srv, "c1"))
	return m, "c1"
}

func TestLocalForward(t *testing.T) {
	m, id := connected(t)
	h, p := echoServer(t)
	info, err := m.StartTunnel(id, TunnelSpec{ID: "t", Kind: TunnelLocal, DestHost: h, DestPort: p})
	if err != nil {
		t.Fatal(err)
	}
	if got := roundTrip(t, info.ListenAddr, "hi"); got != "echo:hi" {
		t.Fatalf("got %q", got)
	}
	list, _ := m.ListTunnels(id)
	if len(list) != 1 || list[0].Total != 1 {
		t.Fatalf("list: %+v", list)
	}
	if err := m.StopTunnel(id, "t"); err != nil {
		t.Fatal(err)
	}
	if c, err := net.DialTimeout("tcp", info.ListenAddr, time.Second); err == nil {
		c.Close()
		t.Fatal("listener still open after stop")
	}
}

func TestRemoteForward(t *testing.T) {
	m, id := connected(t)
	h, p := echoServer(t)
	info, err := m.StartTunnel(id, TunnelSpec{ID: "r", Kind: TunnelRemote, BindHost: "127.0.0.1", BindPort: 0 + freePort(t), DestHost: h, DestPort: p})
	if err != nil {
		t.Fatal(err)
	}
	// The test server listens locally on the requested port.
	if got := roundTrip(t, net.JoinHostPort("127.0.0.1", strconv.Itoa(info.BindPort)), "yo"); got != "echo:yo" {
		t.Fatalf("got %q", got)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestDynamicSOCKS(t *testing.T) {
	m, id := connected(t)
	h, p := echoServer(t)
	info, err := m.StartTunnel(id, TunnelSpec{ID: "d", Kind: TunnelDynamic})
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.DialTimeout("tcp", info.ListenAddr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write([]byte{5, 1, 0})
	resp := make([]byte, 2)
	io.ReadFull(c, resp)
	if resp[0] != 5 || resp[1] != 0 {
		t.Fatalf("method reply %v", resp)
	}
	req := []byte{5, 1, 0, 3, byte(len(h))}
	req = append(req, h...)
	req = append(req, byte(p>>8), byte(p))
	c.Write(req)
	reply := make([]byte, 10)
	io.ReadFull(c, reply)
	if reply[1] != 0 {
		t.Fatalf("connect failed: %v", reply)
	}
	c.Write([]byte("ping"))
	out, _ := io.ReadAll(c)
	if string(out) != "echo:ping" {
		t.Fatalf("got %q", out)
	}
}

// A tunnel forwards at most maxTunnelConns connections; one more is closed
// at once, and stopping the tunnel closes the SOCKS connections still open.
func TestTunnelConnectionCap(t *testing.T) {
	m, id := connected(t)
	info, err := m.StartTunnel(id, TunnelSpec{ID: "d", Kind: TunnelDynamic})
	if err != nil {
		t.Fatal(err)
	}
	conns := make([]net.Conn, 0, maxTunnelConns)
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	for range maxTunnelConns {
		c, err := net.DialTimeout("tcp", info.ListenAddr, 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c) // idle in the SOCKS handshake
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		list, _ := m.ListTunnels(id)
		if len(list) == 1 && list[0].Active == maxTunnelConns {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("active never reached the cap: %+v", list)
		}
		time.Sleep(10 * time.Millisecond)
	}

	extra, err := net.DialTimeout("tcp", info.ListenAddr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	extra.SetDeadline(time.Now().Add(3 * time.Second))
	extra.Write([]byte{5, 1, 0})
	if n, err := extra.Read(make([]byte, 2)); err == nil {
		t.Fatalf("connection over the cap was served (%d bytes)", n)
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("connection over the cap was left open")
	}

	if err := m.StopTunnel(id, "d"); err != nil {
		t.Fatal(err)
	}
	c := conns[0]
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected the connection to be closed")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("connection still open after StopTunnel")
	}
}

func TestTunnelValidation(t *testing.T) {
	m, id := connected(t)
	if _, err := m.StartTunnel(id, TunnelSpec{ID: "x", Kind: TunnelLocal}); err == nil {
		t.Error("local tunnel without destination should fail")
	}
	if _, err := m.StartTunnel(id, TunnelSpec{ID: "x", Kind: "bogus"}); err == nil {
		t.Error("unknown kind should fail")
	}
}

func TestProxyJump(t *testing.T) {
	jump := newTestServer(t, serverOpts{password: "jp"})
	target := newTestServer(t, serverOpts{password: "secret"})
	m, _, _ := newManager(t)
	req := passwordReq(target, "viajump")
	req.Jump = []ConnectRequest{{Host: jump.host, Port: jump.port, Username: "u", AuthType: "password", Password: "jp"}}

	// First attempt stops at the jump host's unknown key, then the target's.
	for i := 0; i < 3; i++ {
		err := m.Connect(req)
		if err == nil {
			break
		}
		hk, ok := err.(*HostKeyError)
		if !ok {
			t.Fatalf("connect: %v", err)
		}
		req.AcceptHostKey = hk.Fingerprint
	}
	if _, err := m.get("viajump"); err != nil {
		t.Fatal("not connected via jump host")
	}
	if err := m.StartShell("viajump", 80, 24); err != nil {
		t.Fatal(err)
	}
}

func TestRecordingCapturesOutput(t *testing.T) {
	m, id := connected(t)
	m.SetRecordingsDir(t.TempDir())
	if err := m.StartShell(id, 100, 30); err != nil {
		t.Fatal(err)
	}
	name, err := m.StartRecording(id, "u@test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.StartRecording(id, "again"); err == nil {
		t.Fatal("second recording accepted")
	}
	if !m.IsRecording(id) {
		t.Fatal("not recording")
	}
	if err := m.WriteInput(id, "ping\r"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var found bool
	for time.Now().Before(deadline) && !found {
		time.Sleep(50 * time.Millisecond)
		rec, err := recording.Read(m.recordingsDir, name)
		if err != nil {
			continue
		}
		for _, e := range rec.Events {
			if e.Kind == "o" && strings.Contains(e.Data, "out:ping") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("shell output was not recorded")
	}
	_ = m.StopRecording(id)
	if m.IsRecording(id) {
		t.Fatal("still recording after stop")
	}
}

func TestStopTunnelClosesOpenSOCKSConnections(t *testing.T) {
	m, id := connected(t)
	info, err := m.StartTunnel(id, TunnelSpec{ID: "d", Kind: TunnelDynamic})
	if err != nil {
		t.Fatal(err)
	}
	// A client that connects but never finishes the SOCKS handshake.
	c, err := net.DialTimeout("tcp", info.ListenAddr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		list, _ := m.ListTunnels(id)
		if len(list) == 1 && list[0].Active == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("connection not accepted: %+v", list)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := m.StopTunnel(id, "d"); err != nil {
		t.Fatal(err)
	}
	// The server side closes the connection right away instead of keeping
	// it open after Stop.
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil || strings.Contains(err.Error(), "timeout") {
		t.Fatalf("connection still open after Stop: %v", err)
	}
}

func TestIsLoopbackBind(t *testing.T) {
	for host, want := range map[string]bool{"127.0.0.1": true, "::1": true, "localhost": true, "0.0.0.0": false, "192.168.1.5": false, "": false} {
		if got := isLoopbackBind(host); got != want {
			t.Errorf("isLoopbackBind(%q) = %v", host, got)
		}
	}
}
