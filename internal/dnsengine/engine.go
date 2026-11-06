// Package dnsengine is a loopback-only DNS sinkhole: blocked domains get
// NXDOMAIN, everything else is forwarded to upstream resolvers.
package dnsengine

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
	"github.com/ubl/adbl/internal/blocklist"
)

// Event is emitted for every handled query (before the response is written).
type Event struct {
	TS      time.Time
	Domain  string
	QType   string
	Blocked bool
	Source  string // blocklist source id when Blocked
}

type Engine struct {
	classifier blocklist.Classifier
	onQuery    func(Event)
	paused     atomic.Int64 // unix nano until which blocking is bypassed; 0 = active

	upsMu     sync.RWMutex
	upstreams []string

	mu      sync.Mutex
	udp     *dns.Server
	tcp     *dns.Server
	udpConn net.PacketConn
	tcpLn   net.Listener
}

// New creates an engine. classifier may be nil (allow-all); onQuery may be nil.
// Upstreams may be bare IPs (port 53 assumed) or host:port.
func New(classifier blocklist.Classifier, upstreams []string, onQuery func(Event)) *Engine {
	if classifier == nil {
		classifier = blocklist.NewSnapshot(nil, nil)
	}
	e := &Engine{classifier: classifier, onQuery: onQuery}
	e.SetUpstreams(upstreams)
	return e
}

// SetUpstreams replaces the upstream list (e.g. after Apply captures the
// machine's original resolvers). Safe to call while serving.
func (e *Engine) SetUpstreams(upstreams []string) {
	ups := make([]string, 0, len(upstreams))
	for _, u := range upstreams {
		if u = upstreamAddr(u); u != "" {
			ups = append(ups, u)
		}
	}
	e.upsMu.Lock()
	e.upstreams = ups
	e.upsMu.Unlock()
}

// Start binds UDP and TCP on addr (e.g. "127.0.0.1:5453"). A bind failure
// returns before any listener is active, so a busy port is reported cleanly.
func (e *Engine) Start(addr string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.udp != nil {
		return fmt.Errorf("engine already started")
	}

	udpConn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("udp listen %s: %w", addr, err)
	}
	// Bind TCP to the UDP socket's actual address so an ephemeral port (":0")
	// resolves to the same port on both nets.
	tcpLn, err := net.Listen("tcp", udpConn.LocalAddr().String())
	if err != nil {
		udpConn.Close()
		return fmt.Errorf("tcp listen %s: %w", udpConn.LocalAddr(), err)
	}

	h := dns.HandlerFunc(e.ServeDNS)
	udp := &dns.Server{PacketConn: udpConn, Handler: h}
	tcp := &dns.Server{Listener: tcpLn, Handler: h}
	e.udp, e.tcp, e.udpConn, e.tcpLn = udp, tcp, udpConn, tcpLn

	go udp.ActivateAndServe()
	go tcp.ActivateAndServe()
	return nil
}

// Addr returns the bound address (useful when port 0 was requested), or "".
func (e *Engine) Addr() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.udpConn == nil {
		return ""
	}
	return e.udpConn.LocalAddr().String()
}

// Running reports whether both listeners are active.
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.udp != nil
}

// SetPausedUntil bypasses blocking until t (zero or past clears the pause).
// The engine keeps serving — system DNS may point at it while paused.
func (e *Engine) SetPausedUntil(t time.Time) {
	var until int64
	if n := t.UnixNano(); t.After(time.Now()) {
		until = n
	}
	e.paused.Store(until)
}

func (e *Engine) pausedNow() bool {
	until := e.paused.Load()
	return until != 0 && time.Now().UnixNano() < until
}

// PausedUntil returns the current pause deadline (zero when active).
func (e *Engine) PausedUntil() time.Time {
	if until := e.paused.Load(); until != 0 {
		return time.Unix(0, until)
	}
	return time.Time{}
}

// Stop shuts both listeners down. Safe to call when not started.
func (e *Engine) Stop() {
	e.mu.Lock()
	udp, tcp, udpConn, tcpLn := e.udp, e.tcp, e.udpConn, e.tcpLn
	e.udp, e.tcp, e.udpConn, e.tcpLn = nil, nil, nil, nil
	e.mu.Unlock()

	if udp != nil {
		udp.Shutdown()
	}
	if tcp != nil {
		tcp.Shutdown()
	}
	if udpConn != nil {
		udpConn.Close()
	}
	if tcpLn != nil {
		tcpLn.Close()
	}
}

// ServeDNS classifies the question, logs the event, and answers: NXDOMAIN
// for blocked names, upstream response otherwise.
func (e *Engine) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	if r == nil || len(r.Question) == 0 {
		return
	}
	q := r.Question[0]
	name := blocklist.Normalize(q.Name)
	blocked, source := false, ""
	if !e.pausedNow() {
		blocked, source = e.classifier.Check(name)
	}
	if e.onQuery != nil {
		e.onQuery(Event{
			TS:      time.Now(),
			Domain:  name,
			QType:   dns.TypeToString[q.Qtype],
			Blocked: blocked,
			Source:  source,
		})
	}

	m := new(dns.Msg)
	m.SetReply(r)
	if blocked {
		m.Rcode = dns.RcodeNameError // NXDOMAIN
		w.WriteMsg(m)
		return
	}
	resp, err := e.forward(r)
	if err != nil {
		m.Rcode = dns.RcodeServerFailure
		w.WriteMsg(m)
		return
	}
	w.WriteMsg(resp)
}

func (e *Engine) forward(r *dns.Msg) (*dns.Msg, error) {
	e.upsMu.RLock()
	ups := append([]string(nil), e.upstreams...)
	e.upsMu.RUnlock()

	var lastErr error
	for _, up := range ups {
		resp, _, err := (&dns.Client{Net: "udp", Timeout: 3 * time.Second}).Exchange(r, up)
		if err != nil {
			lastErr = err
			continue
		}
		if resp != nil && resp.Truncated {
			// Retry over TCP; if that fails keep the truncated UDP answer so
			// the client re-asks us over TCP and we forward again.
			if tcpResp, _, err := (&dns.Client{Net: "tcp", Timeout: 3 * time.Second}).Exchange(r, up); err == nil {
				return tcpResp, nil
			}
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no upstream configured")
	}
	return nil, lastErr
}

func upstreamAddr(s string) string {
	if s == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s
	}
	return net.JoinHostPort(s, "53")
}
