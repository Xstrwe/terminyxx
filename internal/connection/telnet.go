package connection

import (
	"net"
	"sync"
)

// Telnet protocol bytes (RFC 854/855).
const (
	tIAC  = 255
	tDONT = 254
	tDO   = 253
	tWONT = 252
	tWILL = 251
	tSB   = 250
	tSE   = 240

	optEcho  = 1
	optSGA   = 3
	optTType = 24
	optNAWS  = 31
)

// telnetConn wraps a TCP connection: it removes protocol commands from the
// data the terminal sees, answers option negotiation, and escapes outgoing
// data. Only Suppress-Go-Ahead, Echo (remote), window size and terminal type
// are accepted; everything else is refused.
type telnetConn struct {
	net.Conn

	mu       sync.Mutex // guards writes, option state and size
	us, them map[byte]bool
	cols     int
	rows     int

	// parser state (only used by Read)
	state  int
	cmd    byte
	sbOpt  byte
	sbData []byte
	prevCR bool // last data byte written was CR (see Write)
}

const (
	sData = iota
	sIAC
	sCmd // after WILL/WONT/DO/DONT, waiting for the option
	sSB  // inside a subnegotiation
	sSBIAC
)

func newTelnetConn(c net.Conn) *telnetConn {
	return &telnetConn{Conn: c, us: map[byte]bool{}, them: map[byte]bool{}, cols: 80, rows: 24}
}

// Read returns terminal data with the telnet commands filtered out.
func (t *telnetConn) Read(p []byte) (int, error) {
	buf := make([]byte, len(p))
	for {
		n, err := t.Conn.Read(buf)
		out := 0
		for _, b := range buf[:n] {
			if t.feed(b) {
				p[out] = b
				out++
			}
		}
		if out > 0 || err != nil {
			return out, err
		}
	}
}

// feed advances the parser; it reports whether b is terminal data.
func (t *telnetConn) feed(b byte) bool {
	switch t.state {
	case sData:
		if b == tIAC {
			t.state = sIAC
			return false
		}
		return true
	case sIAC:
		switch b {
		case tIAC:
			t.state = sData
			return true // escaped 0xFF data byte
		case tWILL, tWONT, tDO, tDONT:
			t.cmd, t.state = b, sCmd
		case tSB:
			t.state, t.sbData, t.sbOpt = sSB, t.sbData[:0], 0
		default:
			t.state = sData // NOP, GA, ...
		}
		return false
	case sCmd:
		t.state = sData
		t.negotiate(t.cmd, b)
		return false
	case sSB:
		if b == tIAC {
			t.state = sSBIAC
		} else if t.sbOpt == 0 && len(t.sbData) == 0 {
			t.sbOpt = b
		} else if len(t.sbData) < 256 {
			t.sbData = append(t.sbData, b)
		}
		return false
	case sSBIAC:
		if b == tSE {
			t.subnegotiation(t.sbOpt, t.sbData)
			t.state = sData
		} else {
			t.state = sSB // IAC IAC inside SB: ignore the data byte
		}
		return false
	}
	return false
}

func (t *telnetConn) negotiate(cmd, opt byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch cmd {
	case tDO:
		switch opt {
		case optSGA, optNAWS, optTType:
			if !t.us[opt] {
				t.us[opt] = true
				t.sendLocked(tIAC, tWILL, opt)
				if opt == optNAWS {
					t.sendNAWSLocked()
				}
			}
		default:
			t.sendLocked(tIAC, tWONT, opt)
		}
	case tDONT:
		if t.us[opt] {
			t.us[opt] = false
			t.sendLocked(tIAC, tWONT, opt)
		}
	case tWILL:
		switch opt {
		case optEcho, optSGA:
			if !t.them[opt] {
				t.them[opt] = true
				t.sendLocked(tIAC, tDO, opt)
			}
		default:
			t.sendLocked(tIAC, tDONT, opt)
		}
	case tWONT:
		if t.them[opt] {
			t.them[opt] = false
			t.sendLocked(tIAC, tDONT, opt)
		}
	}
}

func (t *telnetConn) subnegotiation(opt byte, data []byte) {
	// TERMINAL-TYPE SEND (1) -> IS xterm-256color
	if opt == optTType && len(data) >= 1 && data[0] == 1 {
		t.mu.Lock()
		defer t.mu.Unlock()
		msg := append([]byte{tIAC, tSB, optTType, 0}, []byte("xterm-256color")...)
		t.sendLocked(append(msg, tIAC, tSE)...)
	}
}

func (t *telnetConn) sendLocked(b ...byte) { _, _ = t.Conn.Write(b) }

func (t *telnetConn) sendNAWSLocked() {
	msg := []byte{tIAC, tSB, optNAWS}
	for _, v := range []int{t.cols, t.rows} {
		hi, lo := byte(v>>8), byte(v)
		for _, x := range []byte{hi, lo} {
			msg = append(msg, x)
			if x == tIAC {
				msg = append(msg, tIAC)
			}
		}
	}
	t.sendLocked(append(msg, tIAC, tSE)...)
}

// Resize tells the server the window size when it asked for it (NAWS).
func (t *telnetConn) Resize(cols, rows int) {
	cols, rows = clampTermSize(cols, rows)
	if cols <= 0 || rows <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cols, t.rows = cols, rows
	if t.us[optNAWS] {
		t.sendNAWSLocked()
	}
}

// Write sends terminal input. 0xFF is doubled, and a bare CR becomes CR NUL
// as the NVT rules require (Enter sends CR; pasted CR LF stays CR LF).
func (t *telnetConn) Write(p []byte) (int, error) {
	out := make([]byte, 0, len(p)+4)
	for _, b := range p {
		switch {
		case t.prevCR && b == '\n':
			out = append(out, '\n')
			t.prevCR = false
			continue
		case t.prevCR:
			out = append(out, 0)
			t.prevCR = false
		}
		switch b {
		case tIAC:
			out = append(out, tIAC, tIAC)
		case '\r':
			out = append(out, '\r')
			t.prevCR = true
		default:
			out = append(out, b)
		}
	}
	if t.prevCR && len(p) > 0 && p[len(p)-1] == '\r' {
		// Resolve at once: waiting for the next write would delay the NUL.
		out = append(out, 0)
		t.prevCR = false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.Conn.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}
