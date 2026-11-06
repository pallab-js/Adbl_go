// Package service exposes the app's capabilities as Wails services:
// the frontend talks only to these, never to internal packages directly.
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/ubl/adbl/internal/blocklist"
	"github.com/ubl/adbl/internal/dnsengine"
	"github.com/ubl/adbl/internal/store"
	"github.com/ubl/adbl/internal/systemdns"
)

// Emitter publishes a typed event to the frontend (safe before app exists).
type Emitter func(event string, data any)

// --- DNS lifecycle -----------------------------------------------------------

type DNSStatus struct {
	Running     bool   `json:"running"`
	Addr        string `json:"addr"`
	PausedUntil int64  `json:"pausedUntil"` // unix seconds; 0 = not paused
}

type DNSService struct {
	eng  *dnsengine.Engine
	emit Emitter
	addr string // configured listen address
}

func NewDNS(eng *dnsengine.Engine, addr string, emit Emitter) *DNSService {
	return &DNSService{eng: eng, emit: emit, addr: addr}
}

func (s *DNSService) Status() DNSStatus {
	st := DNSStatus{Running: s.eng.Running(), Addr: s.addr}
	if st.Running {
		st.Addr = s.eng.Addr()
	}
	if u := s.eng.PausedUntil(); !u.IsZero() {
		st.PausedUntil = u.Unix()
	}
	return st
}

func (s *DNSService) Start() error {
	if s.eng.Running() {
		return nil
	}
	if err := s.eng.Start(s.addr); err != nil {
		return err
	}
	s.emit("dns:state", s.Status())
	return nil
}

func (s *DNSService) Stop() {
	s.eng.Stop()
	s.emit("dns:state", s.Status())
}

// Pause bypasses blocking for the given minutes (0 resumes immediately).
func (s *DNSService) Pause(minutes int) {
	if minutes <= 0 {
		s.eng.SetPausedUntil(time.Time{})
	} else {
		s.eng.SetPausedUntil(time.Now().Add(time.Duration(minutes) * time.Minute))
	}
	s.emit("dns:state", s.Status())
}

func (s *DNSService) SetUpstreams(addrs []string) { s.eng.SetUpstreams(addrs) }

func (s *DNSService) Addr() string { return s.addr }

// --- System DNS (one-click protection) ---------------------------------------

type SystemService struct {
	sys  *systemdns.Service
	dns  *DNSService
	emit Emitter
}

func NewSystem(sys *systemdns.Service, dns *DNSService, emit Emitter) *SystemService {
	return &SystemService{sys: sys, dns: dns, emit: emit}
}

func (s *SystemService) Status() systemdns.Status {
	return s.sys.Status(context.Background())
}

// Enable starts the engine first, then points system DNS at it. The engine
// is always live before the switch — never point DNS at a dead server.
func (s *SystemService) Enable() error {
	if err := s.dns.Start(); err != nil {
		return err
	}
	if err := s.sys.Apply(context.Background(), s.dns.Addr()); err != nil {
		s.emit("system:state", s.Status())
		return err
	}
	// Forward to the machine's original resolvers (never to ourselves).
	if st, ok, _ := s.sys.State(); ok {
		if ups := st.Upstreams(); len(ups) > 0 {
			s.dns.SetUpstreams(ups)
		}
	}
	s.emit("system:state", s.Status())
	s.emit("dns:state", s.dns.Status())
	return nil
}

// Disable restores system DNS, then stops the engine. If the restore is
// denied the engine keeps running — the system still depends on it.
func (s *SystemService) Disable() error {
	err := s.sys.Restore(context.Background())
	if err == nil {
		s.dns.Stop()
	}
	s.emit("system:state", s.Status())
	return err
}

// --- Stats -------------------------------------------------------------------

// StatsTick is the 1s dashboard payload (aggregates only, never raw rows).
type StatsTick struct {
	Today    store.Summary  `json:"today"`
	Day24    store.Summary  `json:"day24"`
	Series60 []store.Bucket `json:"series60"`
	Sources  int            `json:"sources"`
}

type StatsService struct {
	db      *store.DB
	sources func() int
}

func NewStats(db *store.DB, sources func() int) *StatsService {
	return &StatsService{db: db, sources: sources}
}

// RangeSince maps the UI's range strings to a start time.
func RangeSince(r string, now time.Time) (time.Time, error) {
	switch r {
	case "today":
		return store.StartOfDay(now), nil
	case "1h":
		return now.Add(-time.Hour), nil
	case "24h":
		return now.Add(-24 * time.Hour), nil
	case "7d":
		return now.AddDate(0, 0, -7), nil
	case "30d":
		return now.AddDate(0, 0, -30), nil
	case "90d":
		return now.AddDate(0, 0, -90), nil
	}
	return time.Time{}, fmt.Errorf("unknown range %q", r)
}

// BucketSize maps the UI's bucket labels to durations.
func BucketSize(name string) (time.Duration, error) {
	switch name {
	case "1m":
		return time.Minute, nil
	case "5m":
		return 5 * time.Minute, nil
	case "15m":
		return 15 * time.Minute, nil
	case "1h":
		return time.Hour, nil
	}
	return 0, fmt.Errorf("unknown bucket %q", name)
}

func (s *StatsService) Summary(r string) (store.Summary, error) {
	since, err := RangeSince(r, time.Now())
	if err != nil {
		return store.Summary{}, err
	}
	return s.db.Summary(since)
}

func (s *StatsService) Series(r, bucket string) ([]store.Bucket, error) {
	since, err := RangeSince(r, time.Now())
	if err != nil {
		return nil, err
	}
	size, err := BucketSize(bucket)
	if err != nil {
		return nil, err
	}
	return s.db.Series(since, size)
}

// Daily returns local-day buckets for the last `days` days (rollups + today).
func (s *StatsService) Daily(days int) ([]store.Bucket, error) {
	if days < 1 || days > 365 {
		return nil, fmt.Errorf("days out of range: %d", days)
	}
	return s.db.DailySeries(days, time.Now())
}

func (s *StatsService) TopBlocked(r string, n int) ([]store.TopEntry, error) {
	since, err := RangeSince(r, time.Now())
	if err != nil {
		return nil, err
	}
	if n < 1 || n > 100 {
		n = 10
	}
	return s.db.TopBlocked(since, n)
}

// --- Queries -----------------------------------------------------------------

type QueryService struct {
	db *store.DB
}

func NewQueries(db *store.DB) *QueryService { return &QueryService{db: db} }

func (s *QueryService) List(f store.ListFilter) ([]store.Query, error) {
	return s.db.List(f)
}

// --- Blocklists ---------------------------------------------------------------

type BlocklistService struct {
	m  *blocklist.Manager
	st *blocklist.Store
}

func NewBlocklists(m *blocklist.Manager, st *blocklist.Store) *BlocklistService {
	return &BlocklistService{m: m, st: st}
}

func (s *BlocklistService) Sources() []blocklist.Status { return s.m.Statuses() }

// Refresh downloads all enabled sources (seconds; runs synchronously so the
// UI can await it).
func (s *BlocklistService) Refresh() error { return s.m.Refresh(context.Background()) }

func (s *BlocklistService) Toggle(id string, on bool) error { return s.m.SetEnabled(id, on) }

type CheckResult struct {
	Domain  string `json:"domain"`
	Blocked bool   `json:"blocked"`
	Source  string `json:"source"`
}

// Check answers "would this domain be blocked right now?".
func (s *BlocklistService) Check(domain string) (CheckResult, error) {
	d := blocklist.Normalize(domain)
	if _, ok := blocklist.ValidDomain(d); !ok {
		return CheckResult{}, fmt.Errorf("invalid domain %q", domain)
	}
	blocked, src := s.st.Check(d)
	return CheckResult{Domain: d, Blocked: blocked, Source: src}, nil
}

// --- Manual rules ---------------------------------------------------------------

type Rules struct {
	Allow []string `json:"allow"`
	Block []string `json:"block"`
}

type RuleService struct {
	m    *blocklist.Manager
	emit Emitter
}

func NewRules(m *blocklist.Manager, emit Emitter) *RuleService {
	return &RuleService{m: m, emit: emit}
}

func (s *RuleService) List() Rules {
	allow, block := s.m.Rules()
	return Rules{Allow: allow, Block: block}
}

func (s *RuleService) Add(allow bool, domain string) error {
	if err := s.m.AddRule(allow, domain); err != nil {
		return err
	}
	s.emit("rules:changed", s.List())
	return nil
}

func (s *RuleService) Remove(allow bool, domain string) error {
	if err := s.m.RemoveRule(allow, domain); err != nil {
		return err
	}
	s.emit("rules:changed", s.List())
	return nil
}
