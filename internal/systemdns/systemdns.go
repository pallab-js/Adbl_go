// Package systemdns points the system resolver at the local sinkhole and
// restores it afterwards. Every privileged call goes through pkexec with a
// fixed argv (no shell), and the previous DNS config is persisted BEFORE any
// change so a crash can always be repaired.
package systemdns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// State is persisted before Apply changes anything; it is the repair key.
type State struct {
	Backend   string    `json:"backend"` // "resolved"
	Link      string    `json:"link"`
	Servers   []string  `json:"servers"` // pre-apply DNS servers
	Domains   []string  `json:"domains"` // pre-apply search/routing domains
	Listen    string    `json:"listen"`  // our address, e.g. 127.0.0.1:5453
	AppliedAt time.Time `json:"appliedAt"`
}

// Status is what the UI shows in the DNS panel.
type Status struct {
	Supported      bool     `json:"supported"`
	Link           string   `json:"link,omitempty"`
	Applied        bool     `json:"applied"`
	Listen         string   `json:"listen,omitempty"`
	CurrentServers []string `json:"currentServers,omitempty"`
	Message        string   `json:"message,omitempty"`
}

// Runner executes a command; injectable so tests never run real commands.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

type Service struct {
	run       Runner
	elevate   Runner // privileged variant (pkexec prefix)
	look      func(string) error
	statePath string
}

// ifname matches Linux interface names (IFNAMSIZ-1, no shell metacharacters).
var ifname = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)

// New builds the real service. statePath is the repair-state JSON file.
func New(statePath string) *Service {
	return &Service{
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		},
		elevate: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, "pkexec", append([]string{name}, args...)...).CombinedOutput()
		},
		look:      lookPath,
		statePath: statePath,
	}
}

func newWith(run, elevate Runner, look func(string) error, statePath string) *Service {
	return &Service{run: run, elevate: elevate, look: look, statePath: statePath}
}

func lookPath(name string) error {
	_, err := exec.LookPath(name)
	return err
}

// fail prefers the command's own output (pkexec explains denials there)
// over the bare exit status.
func fail(out []byte, err error, what string) error {
	msg := strings.TrimSpace(string(out))
	if msg == "" && err != nil {
		msg = err.Error()
	}
	return fmt.Errorf("%s: %s", what, msg)
}

// Detect reports whether this machine can be managed (systemd-resolved + pkexec).
func (s *Service) Detect(ctx context.Context) error {
	if err := s.look("resolvectl"); err != nil {
		return errors.New("systemd-resolved (resolvectl) not available")
	}
	if err := s.look("pkexec"); err != nil {
		return errors.New("pkexec not available")
	}
	out, err := s.run(ctx, "resolvectl", "status")
	if err != nil {
		return fail(out, err, "systemd-resolved not running")
	}
	return nil
}

// DefaultLink returns the interface carrying the IPv4 default route.
func (s *Service) DefaultLink(ctx context.Context) (string, error) {
	out, err := s.run(ctx, "ip", "route", "show", "default")
	if err != nil {
		return "", fmt.Errorf("ip route: %s", strings.TrimSpace(string(out)))
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			link := fields[i+1]
			if !ifname.MatchString(link) {
				return "", fmt.Errorf("unexpected interface name %q", link)
			}
			return link, nil
		}
	}
	return "", errors.New("no default route found")
}

// Status probes the machine (read-only) plus the state file.
func (s *Service) Status(ctx context.Context) Status {
	st := Status{}
	if err := s.Detect(ctx); err != nil {
		st.Message = err.Error()
		return st
	}
	st.Supported = true
	link, err := s.DefaultLink(ctx)
	if err == nil {
		st.Link = link
	}
	state, ok, err := s.State()
	if err == nil && ok {
		st.Applied = true
		st.Listen = state.Listen
	}
	if link != "" {
		if servers, _, err := s.readLink(ctx, link); err == nil {
			st.CurrentServers = servers
		}
	}
	return st
}

// Upstreams returns the saved servers excluding our own listen address, so
// a machine whose DNS already points at us can never forward in a loop.
func (st State) Upstreams() []string {
	out := make([]string, 0, len(st.Servers))
	for _, s := range st.Servers {
		if s != st.Listen {
			out = append(out, s)
		}
	}
	return out
}

// State loads the persisted repair state.
func (s *Service) State() (State, bool, error) {
	data, err := os.ReadFile(s.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, false, fmt.Errorf("corrupt state file: %w", err)
	}
	if !ifname.MatchString(st.Link) {
		return State{}, false, fmt.Errorf("state file has invalid link %q", st.Link)
	}
	return st, true, nil
}

// Apply points system DNS at listenAddr (engine must already be serving it).
// Order: snapshot current DNS -> persist state -> elevate.
func (s *Service) Apply(ctx context.Context, listenAddr string) error {
	if err := s.Detect(ctx); err != nil {
		return err
	}
	// Idempotence guard: if we already own system DNS, do NOT re-snapshot —
	// that would save our own address as the "original" and break Restore.
	if state, ok, err := s.State(); err != nil {
		return err
	} else if ok {
		servers, _, err := s.readLink(ctx, state.Link)
		if err != nil {
			return err
		}
		if slices.Contains(servers, state.Listen) {
			return nil // already applied
		}
		s.discardState() // stale key: system DNS is no longer ours
	}
	link, err := s.DefaultLink(ctx)
	if err != nil {
		return err
	}
	servers, domains, err := s.readLink(ctx, link)
	if err != nil {
		return err
	}
	state := State{
		Backend:   "resolved",
		Link:      link,
		Servers:   servers,
		Domains:   domains,
		Listen:    listenAddr,
		AppliedAt: time.Now(),
	}
	// Invariant: never change DNS without a persisted rollback key.
	if err := s.saveState(state); err != nil {
		return fmt.Errorf("refusing to change DNS (state not persisted): %w", err)
	}

	if out, err := s.elevate(ctx, "resolvectl", "dns", link, resolvAddr(listenAddr)); err != nil {
		s.discardState() // nothing changed yet
		return fail(out, err, "setting DNS")
	}
	if out, err := s.elevate(ctx, "resolvectl", "domain", link, "~."); err != nil {
		// Partial apply: roll back to saved values (keeps state on failure).
		if rerr := s.Restore(ctx); rerr != nil {
			return fmt.Errorf("%v; rollback failed: %v", fail(out, err, "setting routing domain"), rerr)
		}
		return fail(out, err, "setting routing domain")
	}
	return nil
}

// StillApplied reports whether system DNS currently points at our listen
// address (used on startup to tell "we own DNS" from a stale state file).
func (s *Service) StillApplied(ctx context.Context) (bool, error) {
	state, ok, err := s.State()
	if err != nil || !ok {
		return false, err
	}
	servers, _, err := s.readLink(ctx, state.Link)
	if err != nil {
		return false, err
	}
	return slices.Contains(servers, state.Listen), nil
}

// DiscardState drops a stale repair key (used when system DNS no longer
// points at us, so no elevation is needed to clean up).
func (s *Service) DiscardState() { s.discardState() }

// Restore puts the saved servers/domains back. The state file is kept when
// restoration fails so the next start can retry the repair.
func (s *Service) Restore(ctx context.Context) error {
	state, ok, err := s.State()
	if err != nil {
		return err
	}
	if !ok {
		return nil // nothing to restore
	}
	if err := s.restoreElevated(ctx, state); err != nil {
		return err
	}
	if err := os.Remove(s.statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("state file removal: %w", err)
	}
	return nil
}

func (s *Service) restoreElevated(ctx context.Context, st State) error {
	servers := st.Servers
	if len(servers) == 0 {
		servers = []string{""} // single empty arg clears the list
	}
	args := append([]string{"dns", st.Link}, servers...)
	if out, err := s.elevate(ctx, "resolvectl", args...); err != nil {
		return fail(out, err, "restoring DNS servers")
	}
	domains := st.Domains
	if len(domains) == 0 {
		domains = []string{""}
	}
	args = append([]string{"domain", st.Link}, domains...)
	if out, err := s.elevate(ctx, "resolvectl", args...); err != nil {
		return fail(out, err, "restoring domains")
	}
	return nil
}

// readLink returns the link's current servers and domains.
func (s *Service) readLink(ctx context.Context, link string) ([]string, []string, error) {
	out, err := s.run(ctx, "resolvectl", "dns", link)
	if err != nil {
		return nil, nil, fmt.Errorf("resolvectl dns %s: %s", link, strings.TrimSpace(string(out)))
	}
	servers := parseLinkValues(string(out), link)

	out, err = s.run(ctx, "resolvectl", "domain", link)
	if err != nil {
		return nil, nil, fmt.Errorf("resolvectl domain %s: %s", link, strings.TrimSpace(string(out)))
	}
	return servers, parseLinkValues(string(out), link), nil
}

// parseLinkValues extracts the value part of the "<name> (<link>): a b c"
// line for the given link. Splits on the marker (not the first colon) so
// IPv6 addresses and stray "Global:" lines can't corrupt the result.
func parseLinkValues(out, link string) []string {
	marker := "(" + link + "):"
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, marker); i >= 0 {
			return strings.Fields(line[i+len(marker):])
		}
	}
	return nil
}

// resolvAddr converts a listen address to resolvectl's "ip:port" syntax
// ("127.0.0.1:5453"; IPv6 hosts keep their brackets).
func resolvAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	return net.JoinHostPort(host, port)
}

func (s *Service) saveState(st State) error {
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.statePath)
}

func (s *Service) discardState() {
	os.Remove(s.statePath)
}
