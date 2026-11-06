package blocklist

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/ubl/adbl/internal/config"
)

// maxSourceBytes caps one downloaded source (largest known lists are ~10MB).
const maxSourceBytes = 30 << 20

// Source is a built-in blocklist feed.
type Source struct {
	ID   string
	Name string
	URL  string
}

// DefaultSources: one hosts-format and one Adblock-format feed so both
// parsers run in production. URLs verified reachable.
var DefaultSources = []Source{
	{ID: "stevenblack", Name: "StevenBlack (ads + malware)",
		URL: "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts"},
	{ID: "hagezi-light", Name: "HaGeZi light (ads + trackers)",
		URL: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/adblock/light.txt"},
}

// Status is the per-source state surfaced in the UI.
type Status struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Enabled   bool      `json:"enabled"`
	Domains   int       `json:"domains"`
	LastFetch time.Time `json:"lastFetch"`
	LastError string    `json:"lastError,omitempty"`
}

type Manager struct {
	sources  []Source
	cfg      *config.File
	store    *Store
	client   *http.Client
	onUpdate func() // called after each successful swap (may be nil)

	mu      sync.Mutex
	data    map[string]map[string]struct{} // source id -> last good domains
	status  map[string]*Status
	running bool
}

func NewManager(sources []Source, cfg *config.File, store *Store, onUpdate func()) *Manager {
	if len(sources) == 0 {
		sources = DefaultSources
	}
	m := &Manager{
		sources:  sources,
		cfg:      cfg,
		store:    store,
		client:   &http.Client{Timeout: 60 * time.Second},
		onUpdate: onUpdate,
		data:     make(map[string]map[string]struct{}, len(sources)),
		status:   make(map[string]*Status, len(sources)),
	}
	for _, s := range sources {
		m.status[s.ID] = &Status{ID: s.ID, Name: s.Name, URL: s.URL}
	}
	return m
}

// Refresh downloads every enabled source (keeping last-good data on
// failure), then swaps in a rebuilt snapshot. Concurrent calls are skipped.
func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
	}()

	cfg := m.cfg.Get()
	type result struct {
		id      string
		domains map[string]struct{}
		err     error
	}
	var results []result
	var firstErr error
	for _, s := range m.sources {
		if !enabled(cfg, s.ID) {
			continue
		}
		domains, err := m.fetch(ctx, s.URL)
		results = append(results, result{id: s.ID, domains: domains, err: err})
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", s.ID, err)
		}
	}

	m.mu.Lock()
	for _, r := range results {
		st := m.status[r.id]
		if r.err != nil {
			// Keep last-good data; surface the error in the UI.
			st.LastError = r.err.Error()
			continue
		}
		m.data[r.id] = r.domains
		st.Domains = len(r.domains)
		st.LastFetch = time.Now()
		st.LastError = ""
	}
	m.mu.Unlock()

	m.apply(cfg)
	return firstErr
}

// apply merges cached data + manual rules and swaps the snapshot.
func (m *Manager) apply(cfg config.Config) {
	merged := make(map[string]string, 400_000)
	for _, d := range cfg.Block {
		if d = Normalize(d); d != "" {
			merged[d] = "manual"
		}
	}
	m.mu.Lock()
	for _, s := range m.sources {
		if !enabled(cfg, s.ID) {
			continue
		}
		for d := range m.data[s.ID] {
			if _, seen := merged[d]; !seen {
				merged[d] = s.ID
			}
		}
	}
	m.mu.Unlock()

	m.store.Set(NewSnapshot(merged, cfg.Allow))
	if m.onUpdate != nil {
		m.onUpdate()
	}
}

// StartAutoRefresh refreshes immediately, then every interval until ctx ends.
func (m *Manager) StartAutoRefresh(ctx context.Context, interval time.Duration) {
	go func() {
		m.Refresh(ctx)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.Refresh(ctx)
			}
		}
	}()
}

// SetEnabled toggles a source and rebuilds from cached data (no refetch).
func (m *Manager) SetEnabled(id string, on bool) error {
	if err := m.cfg.Update(func(c *config.Config) {
		if c.EnabledSources == nil {
			c.EnabledSources = map[string]bool{}
		}
		c.EnabledSources[id] = on
	}); err != nil {
		return err
	}
	m.apply(m.cfg.Get())
	return nil
}

// AddRule adds a manual allow or block rule (idempotent) and rebuilds.
func (m *Manager) AddRule(allow bool, domain string) error {
	domain = Normalize(domain)
	if _, ok := ValidDomain(domain); !ok {
		return fmt.Errorf("invalid domain %q", domain)
	}
	err := m.cfg.Update(func(c *config.Config) {
		list := &c.Block
		if allow {
			list = &c.Allow
		}
		for _, d := range *list {
			if d == domain {
				return
			}
		}
		*list = append(*list, domain)
	})
	if err != nil {
		return err
	}
	m.apply(m.cfg.Get())
	return nil
}

// RemoveRule removes a manual allow or block rule and rebuilds.
func (m *Manager) RemoveRule(allow bool, domain string) error {
	domain = Normalize(domain)
	err := m.cfg.Update(func(c *config.Config) {
		list := &c.Block
		if allow {
			list = &c.Allow
		}
		out := (*list)[:0]
		for _, d := range *list {
			if d != domain {
				out = append(out, d)
			}
		}
		*list = out
	})
	if err != nil {
		return err
	}
	m.apply(m.cfg.Get())
	return nil
}

// Statuses returns a copy of all source statuses.
func (m *Manager) Statuses() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.sources))
	cfg := m.cfg.Get()
	for _, s := range m.sources {
		st := *m.status[s.ID]
		st.Enabled = enabled(cfg, s.ID)
		out = append(out, st)
	}
	return out
}

// Rules returns the manual allow and block lists.
func (m *Manager) Rules() (allow, block []string) {
	cfg := m.cfg.Get()
	return cfg.Allow, cfg.Block
}

func (m *Manager) fetch(ctx context.Context, url string) (map[string]struct{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxSourceBytes {
		return nil, fmt.Errorf("source exceeds %d bytes", maxSourceBytes)
	}

	domains := make(map[string]struct{}, 200_000)
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if d, ok := ParseLine(sc.Text()); ok {
			domains[d] = struct{}{}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(domains) == 0 {
		return nil, fmt.Errorf("no domains parsed")
	}
	return domains, nil
}

func enabled(c config.Config, id string) bool {
	if v, ok := c.EnabledSources[id]; ok {
		return v
	}
	return true // default-on for new sources
}
