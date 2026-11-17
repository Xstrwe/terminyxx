package rdpgw

import (
	"encoding/asn1"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"strings"
	"unicode/utf16"
)

// Sign-in tracing for diagnosing NLA failures. With TERMINYXX_RDP_TRACE=1
// the gateway logs the CredSSP and NTLM messages it relays (it terminates
// TLS, so it sees them in the clear): message types, flags, user and domain
// names, field sizes, whether a MIC is present, and the server's errorCode.
// Never the password, hashes or responses.
var traceEnabled = os.Getenv("TERMINYXX_RDP_TRACE") == "1"

// tsRequest is the CredSSP TSRequest (MS-CSSP 2.2.1).
type tsRequest struct {
	Version     int        `asn1:"explicit,tag:0"`
	NegoTokens  []negoData `asn1:"optional,explicit,tag:1"`
	AuthInfo    []byte     `asn1:"optional,explicit,tag:2"`
	PubKeyAuth  []byte     `asn1:"optional,explicit,tag:3"`
	ErrorCode   int64      `asn1:"optional,explicit,tag:4"`
	ClientNonce []byte     `asn1:"optional,explicit,tag:5"`
}

type negoData struct {
	Token []byte `asn1:"explicit,tag:0"`
}

// signInTracer buffers one direction of the stream until the CredSSP
// exchange is over (the first TPKT packet).
type signInTracer struct {
	dir  string
	buf  []byte
	done bool
}

func (t *signInTracer) feed(b []byte) {
	if !traceEnabled || t.done {
		return
	}
	head := b
	if len(head) > 12 {
		head = head[:12]
	}
	log.Printf("rdptrace %s: chunk %dB starts % x", t.dir, len(b), head)
	t.buf = append(t.buf, b...)
	for len(t.buf) > 0 {
		if t.buf[0] == 0x03 { // TPKT: CredSSP is over
			log.Printf("rdptrace %s: CredSSP finished, RDP data follows", t.dir)
			t.done = true
			t.buf = nil
			return
		}
		n, err := derLength(t.buf)
		if err != nil {
			log.Printf("rdptrace %s: unparsable data (%d bytes): %v", t.dir, len(t.buf), err)
			t.done = true
			return
		}
		if n == 0 || len(t.buf) < n {
			return
		}
		log.Printf("rdptrace %s: %s", t.dir, describeTSRequest(t.buf[:n]))
		t.buf = t.buf[n:]
	}
}

func describeTSRequest(b []byte) string {
	var r tsRequest
	if _, err := asn1.Unmarshal(b, &r); err != nil {
		return fmt.Sprintf("TSRequest decode error: %v", err)
	}
	parts := []string{fmt.Sprintf("TSRequest v%d", r.Version)}
	for _, n := range r.NegoTokens {
		parts = append(parts, describeNTLM(n.Token))
	}
	if len(r.PubKeyAuth) > 0 {
		parts = append(parts, fmt.Sprintf("pubKeyAuth=%dB", len(r.PubKeyAuth)))
	}
	if len(r.AuthInfo) > 0 {
		parts = append(parts, fmt.Sprintf("authInfo=%dB", len(r.AuthInfo)))
	}
	if len(r.ClientNonce) > 0 {
		parts = append(parts, fmt.Sprintf("clientNonce=%dB", len(r.ClientNonce)))
	}
	if r.ErrorCode != 0 {
		parts = append(parts, fmt.Sprintf("errorCode=0x%08X (%s)", uint32(r.ErrorCode), ntStatusText(uint32(r.ErrorCode))))
	}
	return strings.Join(parts, " | ")
}

func describeNTLM(tok []byte) string {
	if len(tok) < 12 || string(tok[:8]) != "NTLMSSP\x00" {
		if len(tok) > 0 && tok[0] == 0x60 || len(tok) > 0 && tok[0] == 0xa1 || len(tok) > 0 && tok[0] == 0xa0 {
			return fmt.Sprintf("SPNEGO token %dB", len(tok))
		}
		return fmt.Sprintf("non-NTLM token %dB", len(tok))
	}
	le := binary.LittleEndian
	secbuf := func(off int) (int, int) {
		if len(tok) < off+8 {
			return 0, 0
		}
		return int(le.Uint16(tok[off:])), int(le.Uint32(tok[off+4:]))
	}
	str := func(off int, unicode bool) string {
		l, o := secbuf(off)
		if l == 0 || o+l > len(tok) {
			return ""
		}
		raw := tok[o : o+l]
		if !unicode {
			return string(raw)
		}
		u := make([]uint16, len(raw)/2)
		for i := range u {
			u[i] = le.Uint16(raw[2*i:])
		}
		return string(utf16.Decode(u))
	}
	switch le.Uint32(tok[8:]) {
	case 1:
		var flags uint32
		if len(tok) >= 16 {
			flags = le.Uint32(tok[12:])
		}
		return fmt.Sprintf("NTLM NEGOTIATE flags=0x%08X", flags)
	case 2:
		if len(tok) < 48 {
			return "NTLM CHALLENGE (short)"
		}
		flags := le.Uint32(tok[20:])
		tiLen, _ := secbuf(40)
		return fmt.Sprintf("NTLM CHALLENGE flags=0x%08X target=%q targetInfo=%dB", flags, str(12, flags&1 != 0), tiLen)
	case 3:
		if len(tok) < 64 {
			return "NTLM AUTHENTICATE (short)"
		}
		flags := le.Uint32(tok[60:])
		unicode := flags&1 != 0
		lmLen, _ := secbuf(12)
		ntLen, _ := secbuf(20)
		keyLen, _ := secbuf(52)
		mic := "absent"
		if len(tok) >= 88 {
			mic = "zero"
			for _, c := range tok[72:88] {
				if c != 0 {
					mic = "present"
					break
				}
			}
		}
		return fmt.Sprintf("NTLM AUTHENTICATE flags=0x%08X user=%q domain=%q workstation=%q lm=%dB nt=%dB sessionKey=%dB mic=%s",
			flags, str(36, unicode), str(28, unicode), str(44, unicode), lmLen, ntLen, keyLen, mic)
	}
	return "NTLM unknown message"
}

func ntStatusText(s uint32) string {
	switch s {
	case 0xC000006D:
		return "logon failure"
	case 0xC000006A:
		return "wrong password"
	case 0xC0000064:
		return "no such user"
	case 0xC000006E:
		return "account restriction"
	case 0xC0000070:
		return "workstation restriction"
	case 0xC0000071:
		return "password expired"
	case 0xC0000072:
		return "account disabled"
	case 0xC0000193:
		return "account expired"
	case 0xC0000224:
		return "password must change"
	case 0xC0000234:
		return "account locked out"
	case 0xC0000022:
		return "access denied"
	case 0x80090308:
		return "SEC_E_INVALID_TOKEN"
	case 0x8009030C:
		return "SEC_E_LOGON_DENIED"
	case 0x80090302:
		return "SEC_E_UNSUPPORTED_FUNCTION"
	case 0x80090330:
		return "SEC_E_MESSAGE_ALTERED"
	}
	return "unknown"
}
