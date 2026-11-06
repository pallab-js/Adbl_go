package dnsengine

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/ubl/adbl/internal/blocklist"
)

// startFakeUpstream answers every A query with 9.9.9.9 and counts queries.
func startFakeUpstream(t *testing.T, hits *atomic.Int32) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	h := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		hits.Add(1)
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) > 0 && r.Question[0].Qtype == dns.TypeA {
			m.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.ParseIP("9.9.9.9"),
			}}
		}
		w.WriteMsg(m)
	})
	srv := &dns.Server{PacketConn: pc, Handler: h}
	go srv.ActivateAndServe()
	go (&dns.Server{Listener: ln, Handler: h}).ActivateAndServe()
	t.Cleanup(func() { srv.Shutdown(); ln.Close() })
	return pc.LocalAddr().String()
}

// startEngine runs an engine on an ephemeral loopback port.
func startEngine(t *testing.T, classifier blocklist.Classifier, upstreams []string, onQuery func(Event)) (*Engine, string) {
	t.Helper()
	e := New(classifier, upstreams, onQuery)
	if err := e.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(e.Stop)
	return e, e.Addr()
}

func exchange(t *testing.T, net string, addr, name string) *dns.Msg {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeA)
	resp, _, err := (&dns.Client{Net: net, Timeout: 3 * time.Second}).Exchange(m, addr)
	if err != nil {
		t.Fatalf("exchange %s %s: %v", net, name, err)
	}
	return resp
}

func TestBlockedQueryReturnsNXDomainWithoutForwarding(t *testing.T) {
	var upstreamHits atomic.Int32
	up := startFakeUpstream(t, &upstreamHits)

	// collector guards events: the server goroutine appends without any
	// synchronization the race detector can see across the socket.
	var mu sync.Mutex
	var got []Event
	collect := func(ev Event) { mu.Lock(); got = append(got, ev); mu.Unlock() }
	events := func() []Event { mu.Lock(); defer mu.Unlock(); return append([]Event(nil), got...) }

	classifier := blocklist.NewSnapshot(map[string]string{"ads.blocked.test": "src1"}, nil)
	_, addr := startEngine(t, classifier, []string{up}, collect)

	resp := exchange(t, "udp", addr, "ad.ads.blocked.test")
	if resp.Rcode != dns.RcodeNameError {
		t.Fatalf("rcode = %d, want NXDOMAIN(%d)", resp.Rcode, dns.RcodeNameError)
	}
	if len(resp.Answer) != 0 {
		t.Fatalf("blocked answer should be empty, got %v", resp.Answer)
	}
	if n := upstreamHits.Load(); n != 0 {
		t.Fatalf("upstream hit %d times, want 0", n)
	}
	collected := events()
	if len(collected) != 1 || !collected[0].Blocked || collected[0].Domain != "ad.ads.blocked.test" || collected[0].Source != "src1" {
		t.Fatalf("bad event: %+v", collected)
	}
}

func TestAllowedQueryIsForwarded(t *testing.T) {
	var upstreamHits atomic.Int32
	up := startFakeUpstream(t, &upstreamHits)

	var mu sync.Mutex
	var got []Event
	_, addr := startEngine(t, nil, []string{up}, func(ev Event) { mu.Lock(); got = append(got, ev); mu.Unlock() })

	resp := exchange(t, "udp", addr, "www.example.org")
	if resp.Rcode != dns.RcodeSuccess {
		t.Fatalf("rcode = %d, want NOERROR", resp.Rcode)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("answers = %d, want 1", len(resp.Answer))
	}
	if n := upstreamHits.Load(); n != 1 {
		t.Fatalf("upstream hits = %d, want 1", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Blocked || got[0].Domain != "www.example.org" {
		t.Fatalf("bad event: %+v", got)
	}
}

func TestTCPForwarding(t *testing.T) {
	var upstreamHits atomic.Int32
	up := startFakeUpstream(t, &upstreamHits)
	_, addr := startEngine(t, nil, []string{up}, nil)

	resp := exchange(t, "tcp", addr, "tcp.example.org")
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("tcp forward failed: rcode=%d answers=%d", resp.Rcode, len(resp.Answer))
	}
}

func TestUpstreamFailover(t *testing.T) {
	var upstreamHits atomic.Int32
	up := startFakeUpstream(t, &upstreamHits)
	// First upstream is dead; engine must fall through to the live one.
	_, addr := startEngine(t, nil, []string{"127.0.0.1:1", up}, nil)

	resp := exchange(t, "udp", addr, "failover.example.org")
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("failover failed: rcode=%d answers=%d", resp.Rcode, len(resp.Answer))
	}
}

func TestNoUpstreamReturnsServFail(t *testing.T) {
	_, addr := startEngine(t, nil, nil, nil)
	resp := exchange(t, "udp", addr, "anything.example.org")
	if resp.Rcode != dns.RcodeServerFailure {
		t.Fatalf("rcode = %d, want SERVFAIL", resp.Rcode)
	}
}

func TestStartStopAndBusyPort(t *testing.T) {
	e := New(nil, nil, nil)
	if err := e.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("start: %v", err)
	}
	addr := e.Addr()
	if addr == "" {
		t.Fatal("Addr() empty after start")
	}
	if err := e.Start(addr); err == nil {
		t.Fatal("second Start on same addr should fail")
	}
	e.Stop()
	e.Stop() // idempotent

	// Port must be free again: a new engine can bind it.
	e2 := New(nil, nil, nil)
	if err := e2.Start(addr); err != nil {
		t.Fatalf("restart after stop: %v", err)
	}
	e2.Stop()
}
