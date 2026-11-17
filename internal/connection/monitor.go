package connection

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// HostStats is a snapshot of a Linux host's load, read over a separate SSH
// exec channel (no extra connection, nothing installed on the server).
type HostStats struct {
	// CPUPercent is -1 for the first sample: it needs two readings to compare.
	CPUPercent float64 `json:"cpuPercent"`
	Cores      int     `json:"cores"`
	Load1      float64 `json:"load1"`
	Load5      float64 `json:"load5"`
	Load15     float64 `json:"load15"`
	MemTotal   int64   `json:"memTotal"` // bytes
	MemUsed    int64   `json:"memUsed"`
	SwapTotal  int64   `json:"swapTotal"`
	SwapUsed   int64   `json:"swapUsed"`
	DiskTotal  int64   `json:"diskTotal"` // the / filesystem
	DiskUsed   int64   `json:"diskUsed"`
	UptimeSec  int64   `json:"uptimeSec"`
	// RxPerSec and TxPerSec are -1 for the first sample.
	RxPerSec float64 `json:"rxPerSec"`
	TxPerSec float64 `json:"txPerSec"`
}

// ErrMonitorUnsupported means the server did not answer like Linux (/proc).
var ErrMonitorUnsupported = errors.New("monitoring needs a Linux host")

const statsCommand = `echo ==stat; head -n1 /proc/stat; echo ==cpus; grep -c '^processor' /proc/cpuinfo; ` +
	`echo ==mem; grep -E '^(MemTotal|MemAvailable|SwapTotal|SwapFree):' /proc/meminfo; ` +
	`echo ==load; cat /proc/loadavg; echo ==up; cat /proc/uptime; ` +
	`echo ==df; df -P -k / | tail -n1; echo ==net; cat /proc/net/dev`

// monitorState keeps the previous counters for rate calculations.
type monitorState struct {
	mu      sync.Mutex
	prevCPU [2]uint64 // total, idle
	prevRx  uint64
	prevTx  uint64
	prevAt  time.Time
	have    bool
}

const execTimeout = 8 * time.Second

// Stats reads one snapshot. Rates compare with the previous call.
func (c *ManagedConnection) Stats() (HostStats, error) {
	client := c.Client()
	if client == nil {
		return HostStats{}, errors.New("not connected")
	}
	out, err := runExec(client, statsCommand, execTimeout)
	if err != nil {
		return HostStats{}, err
	}
	return c.monitor.parse(out, time.Now())
}

// maxExecOutput bounds what runExec keeps from a command's output.
const maxExecOutput = 1 << 20

// capWriter writes up to n bytes to w and silently drops the rest, so a
// misbehaving server cannot make the client buffer unbounded output.
type capWriter struct {
	w io.Writer
	n int
}

func (c *capWriter) Write(p []byte) (int, error) {
	if c.n > 0 {
		k := min(len(p), c.n)
		if _, err := c.w.Write(p[:k]); err != nil {
			return 0, err
		}
		c.n -= k
	}
	return len(p), nil
}

// runExec runs a command on a fresh session and returns its stdout.
func runExec(client *ssh.Client, cmd string, timeout time.Duration) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		var buf bytes.Buffer
		// The output comes from the server: keep at most maxExecOutput bytes.
		session.Stdout = &capWriter{w: &buf, n: maxExecOutput}
		err := session.Run(cmd)
		done <- result{buf.Bytes(), err}
	}()
	select {
	case r := <-done:
		session.Close()
		if r.err != nil && len(r.out) == 0 {
			return "", r.err
		}
		return string(r.out), nil
	case <-time.After(timeout):
		session.Close()
		return "", errors.New("the server did not answer in time")
	}
}

func (m *monitorState) parse(out string, now time.Time) (HostStats, error) {
	sections := map[string][]string{}
	cur := ""
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if strings.HasPrefix(line, "==") {
			cur = strings.TrimPrefix(line, "==")
			continue
		}
		if cur != "" && strings.TrimSpace(line) != "" {
			sections[cur] = append(sections[cur], line)
		}
	}
	st := HostStats{CPUPercent: -1, RxPerSec: -1, TxPerSec: -1}

	cpuLine := first(sections["stat"])
	f := strings.Fields(cpuLine)
	if len(f) < 5 || f[0] != "cpu" {
		return st, ErrMonitorUnsupported
	}
	var total, idle uint64
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		if i < 8 { // user nice system idle iowait irq softirq steal
			total += n
		}
		if i == 3 || i == 4 {
			idle += n
		}
	}
	st.Cores, _ = strconv.Atoi(strings.TrimSpace(first(sections["cpus"])))

	for _, l := range sections["mem"] {
		k, v := kv(l)
		switch k {
		case "MemTotal":
			st.MemTotal = v
		case "MemAvailable":
			st.MemUsed = v // temporarily: available
		case "SwapTotal":
			st.SwapTotal = v
		case "SwapFree":
			st.SwapUsed = v // temporarily: free
		}
	}
	st.MemUsed = max64(0, st.MemTotal-st.MemUsed)
	st.SwapUsed = max64(0, st.SwapTotal-st.SwapUsed)

	if lf := strings.Fields(first(sections["load"])); len(lf) >= 3 {
		st.Load1, _ = strconv.ParseFloat(lf[0], 64)
		st.Load5, _ = strconv.ParseFloat(lf[1], 64)
		st.Load15, _ = strconv.ParseFloat(lf[2], 64)
	}
	if uf := strings.Fields(first(sections["up"])); len(uf) >= 1 {
		u, _ := strconv.ParseFloat(uf[0], 64)
		st.UptimeSec = int64(u)
	}
	if df := strings.Fields(first(sections["df"])); len(df) >= 4 {
		t, _ := strconv.ParseInt(df[1], 10, 64)
		u, _ := strconv.ParseInt(df[2], 10, 64)
		st.DiskTotal, st.DiskUsed = t*1024, u*1024
	}
	var rx, tx uint64
	for _, l := range sections["net"] {
		name, rest, ok := strings.Cut(l, ":")
		if !ok || strings.TrimSpace(name) == "lo" {
			continue
		}
		nf := strings.Fields(rest)
		if len(nf) >= 9 {
			r, _ := strconv.ParseUint(nf[0], 10, 64)
			t, _ := strconv.ParseUint(nf[8], 10, 64)
			rx += r
			tx += t
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.have {
		if dt := total - m.prevCPU[0]; total > m.prevCPU[0] && dt > 0 {
			di := idle - m.prevCPU[1]
			if idle < m.prevCPU[1] {
				di = 0
			}
			st.CPUPercent = clamp(100 * (1 - float64(di)/float64(dt)))
		}
		if secs := now.Sub(m.prevAt).Seconds(); secs > 0 && rx >= m.prevRx && tx >= m.prevTx {
			st.RxPerSec = float64(rx-m.prevRx) / secs
			st.TxPerSec = float64(tx-m.prevTx) / secs
		}
	}
	m.prevCPU = [2]uint64{total, idle}
	m.prevRx, m.prevTx, m.prevAt, m.have = rx, tx, now, true
	return st, nil
}

func first(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

// kv parses "MemTotal:  16384 kB" into bytes.
func kv(line string) (string, int64) {
	k, rest, ok := strings.Cut(line, ":")
	if !ok {
		return "", 0
	}
	f := strings.Fields(rest)
	if len(f) == 0 {
		return k, 0
	}
	n, _ := strconv.ParseInt(f[0], 10, 64)
	if len(f) > 1 && strings.EqualFold(f[1], "kB") {
		n *= 1024
	}
	return k, n
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// HostStats returns a snapshot for connection id.
func (m *ConnectionManager) HostStats(id string) (HostStats, error) {
	if m.gate.Locked() {
		return HostStats{}, ErrLocked
	}
	conn, err := m.get(id)
	if err != nil {
		return HostStats{}, err
	}
	st, err := conn.Stats()
	if err != nil {
		return st, fmt.Errorf("%w", err)
	}
	return st, nil
}
