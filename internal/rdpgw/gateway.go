package rdpgw

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"

	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	ticketTTL   = time.Minute
	dialTimeout = 15 * time.Second
	// maxMessage bounds one WebSocket message from the client.
	maxMessage = 16 << 20
)

// Gateway serves the RDCleanPath WebSocket on 127.0.0.1. Each session needs
// a single-use ticket issued by the app for one tab and one destination, so
// nothing else on the machine can use it as an open relay.
type Gateway struct {
	trust *TrustStore

	mu      sync.Mutex
	ln      net.Listener
	srv     *http.Server
	tickets map[string]*ticket
	live    map[string]*liveSession // by tab id
	errs    map[string]string       // last failure by tab id
}

type ticket struct {
	tabID   string
	host    string
	port    int
	expires time.Time
}

type liveSession struct {
	ws   *websocket.Conn
	conn net.Conn
}

func (l *liveSession) close() {
	if l.ws != nil {
		_ = l.ws.Close()
	}
	if l.conn != nil {
		_ = l.conn.Close()
	}
}

func New(trust *TrustStore) *Gateway {
	return &Gateway{trust: trust, tickets: map[string]*ticket{}, live: map[string]*liveSession{}, errs: map[string]string{}}
}

func (g *Gateway) Trust() *TrustStore { return g.trust }

// ensureListening starts the HTTP server on first use.
func (g *Gateway) ensureListening() (string, error) {
	if g.ln != nil {
		return g.ln.Addr().String(), nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/rdp", g.serveWS)
	g.ln = ln
	g.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = g.srv.Serve(ln) }()
	return ln.Addr().String(), nil
}

// Issue creates a ticket for tabID to reach host:port and returns the
// WebSocket URL and the token the client must send as proxy_auth.
func (g *Gateway) Issue(tabID, host string, port int) (url, token string, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	addr, err := g.ensureListening()
	if err != nil {
		return "", "", err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(buf)
	now := time.Now()
	for k, t := range g.tickets {
		if now.After(t.expires) || t.tabID == tabID {
			delete(g.tickets, k)
		}
	}
	g.tickets[token] = &ticket{tabID: tabID, host: host, port: port, expires: now.Add(ticketTTL)}
	delete(g.errs, tabID)
	return "ws://" + addr + "/rdp", token, nil
}

func (g *Gateway) take(token string) *ticket {
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, t := range g.tickets {
		if subtle.ConstantTimeCompare([]byte(k), []byte(token)) == 1 {
			delete(g.tickets, k)
			if time.Now().After(t.expires) {
				return nil
			}
			return t
		}
	}
	return nil
}

// LastError returns why the most recent session of tabID failed on the
// gateway side (the client only learns an error code).
func (g *Gateway) LastError(tabID string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.errs[tabID]
}

func (g *Gateway) setErr(tabID string, err error) {
	g.mu.Lock()
	g.errs[tabID] = err.Error()
	g.mu.Unlock()
}

// Close ends the session of tabID, if any.
func (g *Gateway) Close(tabID string) {
	g.mu.Lock()
	l := g.live[tabID]
	delete(g.live, tabID)
	for k, t := range g.tickets {
		if t.tabID == tabID {
			delete(g.tickets, k)
		}
	}
	g.mu.Unlock()
	if l != nil {
		l.close()
	}
}

// Shutdown closes every session and the listener.
func (g *Gateway) Shutdown() {
	g.mu.Lock()
	live := g.live
	g.live = map[string]*liveSession{}
	srv := g.srv
	g.mu.Unlock()
	for _, l := range live {
		l.close()
	}
	if srv != nil {
		_ = srv.Close()
	}
}

// Probe connects to the server, negotiates TLS and returns its certificate
// chain without starting a session.
func Probe(ctx context.Context, host string, port int) ([]*x509.Certificate, error) {
	conn, _, neg, err := dialServer(ctx, host, port, probeConnectionRequest())
	if err != nil {
		return nil, err
	}
	if neg.failed {
		return nil, errors.New(negotiationFailureText(neg.code))
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates, nil
}

// dialServer sends the X.224 connection request and, if the server selected
// TLS or CredSSP, upgrades to TLS. The returned conn is nil when negotiation
// failed; resp is the server's X.224 confirm either way.
func dialServer(ctx context.Context, host string, port int, x224 []byte) (*tls.Conn, []byte, negotiation, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	d := net.Dialer{KeepAlive: 30 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, nil, negotiation{}, &dialError{err}
	}
	stop := context.AfterFunc(ctx, func() { _ = raw.SetDeadline(time.Now()) })
	defer stop()

	if _, err := raw.Write(x224); err != nil {
		raw.Close()
		return nil, nil, negotiation{}, err
	}
	resp, err := readTPKT(raw)
	if err != nil {
		raw.Close()
		return nil, nil, negotiation{}, fmt.Errorf("read connection confirm: %w", err)
	}
	neg, err := parseConnectionConfirm(resp)
	if err != nil {
		raw.Close()
		return nil, nil, negotiation{}, err
	}
	if neg.failed {
		raw.Close()
		return nil, resp, neg, nil
	}
	if neg.selected == protocolRDP {
		raw.Close()
		return nil, nil, neg, errors.New("the server only offers legacy RDP security without TLS, which the built-in client does not support")
	}
	cfg := &tls.Config{
		// The chain is checked against the trust store right after the
		// handshake (TrustStore.Check), since RDP servers are usually
		// self-signed.
		InsecureSkipVerify: true, //nolint:gosec
		MinVersion:         tls.VersionTLS12,
		// Windows' RDP server completes a TLS 1.3 handshake and then sends
		// an internal_error alert, so CredSSP never starts (it logs a
		// misleading ERROR_LOGON_FAILURE). mstsc and Windows App use 1.2.
		MaxVersion: tls.VersionTLS12,
	}
	if net.ParseIP(host) == nil {
		cfg.ServerName = host
	}
	tc := tls.Client(raw, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, nil, neg, &tlsError{err}
	}
	return tc, resp, neg, nil
}

type dialError struct{ err error }

func (e *dialError) Error() string { return e.err.Error() }
func (e *dialError) Unwrap() error { return e.err }

type tlsError struct{ err error }

func (e *tlsError) Error() string { return "TLS handshake failed: " + e.err.Error() }
func (e *tlsError) Unwrap() error { return e.err }

var upgrader = websocket.Upgrader{
	ReadBufferSize:  64 << 10,
	WriteBufferSize: 64 << 10,
	// The single-use ticket authorizes the session; the page origin differs
	// between platforms and wails dev.
	CheckOrigin: func(*http.Request) bool { return true },
}

func (g *Gateway) serveWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(maxMessage)
	if err := g.session(r.Context(), ws); err != nil {
		log.Printf("rdpgw: %v", err)
	}
}

func (g *Gateway) session(ctx context.Context, ws *websocket.Conn) error {
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(30 * time.Second))
	var buf []byte
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		if mt != websocket.BinaryMessage {
			return errors.New("unexpected text message")
		}
		buf = append(buf, data...)
		n, err := derLength(buf)
		if err != nil {
			return err
		}
		if n > maxMessage {
			return errors.New("RDCleanPath request too large")
		}
		if n > 0 && len(buf) >= n {
			if len(buf) > n {
				return errors.New("data after the RDCleanPath request")
			}
			break
		}
	}
	_ = ws.SetReadDeadline(time.Time{})

	req, err := decodePDU(buf)
	if err != nil {
		return err
	}
	t := g.take(req.ProxyAuth)
	if t == nil {
		_ = ws.WriteMessage(websocket.BinaryMessage, encodeError(pduError{Code: errGeneral, HTTPStatus: 401}))
		return errors.New("invalid or expired ticket")
	}
	fail := func(code int, e error) error {
		g.setErr(t.tabID, e)
		_ = ws.WriteMessage(websocket.BinaryMessage, encodeError(pduError{Code: code}))
		return e
	}
	if req.PreconnectionBlob != "" || !validConnectionRequest(req.X224) {
		return fail(errGeneral, errors.New("unsupported connection request"))
	}

	conn, resp, neg, err := dialServer(ctx, t.host, t.port, req.X224)
	if err != nil {
		var de *dialError
		var te *tlsError
		var dnsErr *net.DNSError
		switch {
		case errors.As(err, &dnsErr):
			return fail(errDNSFailed, fmt.Errorf("could not resolve %s", t.host))
		case errors.As(err, &de):
			return fail(errTargetUnreachable, err)
		case errors.As(err, &te):
			return fail(errTLSFailed, err)
		}
		return fail(errGeneral, err)
	}
	if neg.failed {
		g.setErr(t.tabID, errors.New(negotiationFailureText(neg.code)))
		_ = ws.WriteMessage(websocket.BinaryMessage, encodeNegotiationError(resp))
		return nil
	}

	dest := net.JoinHostPort(t.host, strconv.Itoa(t.port))
	certs := conn.ConnectionState().PeerCertificates
	if err := g.trust.Check(dest, t.host, certs); err != nil {
		conn.Close()
		return fail(errUntrusted, err)
	}
	chain := make([][]byte, len(certs))
	for i, c := range certs {
		chain[i] = c.Raw
	}
	out, err := encodeResponse(conn.RemoteAddr().String(), resp, chain)
	if err != nil {
		conn.Close()
		return fail(errGeneral, err)
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, out); err != nil {
		conn.Close()
		return err
	}

	l := &liveSession{ws: ws, conn: conn}
	g.mu.Lock()
	prev := g.live[t.tabID]
	g.live[t.tabID] = l
	g.mu.Unlock()
	if prev != nil {
		prev.close()
	}
	defer func() {
		g.mu.Lock()
		if g.live[t.tabID] == l {
			delete(g.live, t.tabID)
		}
		g.mu.Unlock()
		l.close()
	}()
	relay(ws, conn)
	return nil
}

// relay copies the RDP stream both ways until either side closes.
func relay(ws *websocket.Conn, conn net.Conn) {
	start := time.Now()
	if traceEnabled {
		log.Printf("rdptrace: relay started")
	}
	up := &signInTracer{dir: "client->server"}
	down := &signInTracer{dir: "server->client"}
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				if traceEnabled {
					log.Printf("rdptrace client->server: websocket ended after %v: %v", time.Since(start), err)
				}
				return
			}
			if mt != websocket.BinaryMessage {
				if traceEnabled {
					log.Printf("rdptrace client->server: ignored message type %d (%dB)", mt, len(data))
				}
				continue
			}
			up.feed(data)
			if _, err := conn.Write(data); err != nil {
				return
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 64<<10)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				down.feed(buf[:n])
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				if traceEnabled && !down.done {
					log.Printf("rdptrace server->client: connection ended during sign-in after %v: %v", time.Since(start), err)
				}
				_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
				return
			}
		}
	}()
	<-done
	_ = conn.Close()
	_ = ws.Close()
	<-done
}
