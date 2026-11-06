package systemdns

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fake struct {
	mu    sync.Mutex
	log   []string // ordered: "run: …" / "elev: …" / "state: …"
	run   func(name string, args ...string) ([]byte, error)
	elev  func(name string, args ...string) ([]byte, error)
	state string // statePath, recorded by elev handlers to prove ordering
}

func (f *fake) runner(priv bool) Runner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		cmd := strings.Join(append([]string{name}, args...), " ")
		tag := "run"
		if priv {
			tag = "elev"
		}
		f.mu.Lock()
		f.log = append(f.log, tag+": "+cmd)
		if priv && f.state != "" {
			if _, err := os.Stat(f.state); err == nil {
				f.log = append(f.log, "state: present")
			} else {
				f.log = append(f.log, "state: MISSING")
			}
		}
		fn := f.run
		if priv {
			fn = f.elev
		}
		f.mu.Unlock()
		if fn == nil {
			return nil, nil
		}
		return fn(name, args...)
	}
}

func (f *fake) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

// newFakeService wires a Service against a fake machine whose default link
// is enp0s1 with the same output shape observed on a real systemd 259 host.
func newFakeService(t *testing.T) (*fake, *Service, string) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "state.json")
	f := &fake{state: statePath}
	f.run = func(name string, args ...string) ([]byte, error) {
		cmd := strings.Join(append([]string{name}, args...), " ")
		switch cmd {
		case "resolvectl status":
			return []byte("Global\n  resolv.conf mode: stub\n"), nil
		case "ip route show default":
			return []byte("default via 192.168.29.1 dev enp0s1 proto dhcp src 192.168.29.124 metric 100 \n"), nil
		case "resolvectl dns enp0s1":
			return []byte("Link 2 (enp0s1): 192.168.29.1 2405:201:a805:c923::c0a8:1d01\n"), nil
		case "resolvectl domain enp0s1":
			return []byte("Link 2 (enp0s1):\n"), nil
		}
		return []byte("unexpected: " + cmd), errors.New("exit status 1")
	}
	f.elev = func(string, ...string) ([]byte, error) { return nil, nil }
	svc := newWith(f.runner(false), f.runner(true), func(string) error { return nil }, statePath)
	return f, svc, statePath
}

func TestApplyTwiceIsNoop(t *testing.T) {
	f, svc, statePath := newFakeService(t)
	if err := svc.Apply(context.Background(), "127.0.0.1:5453"); err != nil {
		t.Fatal(err)
	}
	firstElev := countPrefix(f.calls(), "elev:")

	// Machine now reports OUR address as current (post-apply state).
	f.run = func(name string, args ...string) ([]byte, error) {
		cmd := strings.Join(append([]string{name}, args...), " ")
		switch cmd {
		case "resolvectl status":
			return []byte("Global\n"), nil
		case "resolvectl dns enp0s1":
			return []byte("Link 2 (enp0s1): 127.0.0.1:5453\n"), nil
		case "resolvectl domain enp0s1":
			return []byte("Link 2 (enp0s1): ~.\n"), nil
		}
		return []byte("unexpected: " + cmd), errors.New("exit 1")
	}

	if err := svc.Apply(context.Background(), "127.0.0.1:5453"); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if n := countPrefix(f.calls(), "elev:"); n != firstElev {
		t.Fatalf("second Apply elevated %d extra times — must be a no-op: %v",
			n-firstElev, f.calls())
	}
	// Original servers must still be saved (not overwritten by our own addr).
	st, ok, err := svc.State()
	if err != nil || !ok {
		t.Fatalf("state lost: ok=%v err=%v", ok, err)
	}
	if len(st.Servers) != 2 || st.Servers[0] != "192.168.29.1" {
		t.Fatalf("state overwritten with our address: %v", st.Servers)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file missing: %v", err)
	}
}

func TestApplyWithStaleStateRediscoversOriginal(t *testing.T) {
	f, svc, _ := newFakeService(t)
	// State file exists from a previous run, but system DNS was reverted
	// externally (resolvectl revert): machine reports original servers.
	if err := svc.saveState(State{
		Backend: "resolved", Link: "enp0s1", Listen: "127.0.0.1:5453",
		Servers: []string{"192.168.29.1"}, // stale snapshot
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.Apply(context.Background(), "127.0.0.1:5453"); err != nil {
		t.Fatal(err)
	}
	// Must have re-snapshotted the REAL current servers (v4+v6), not kept
	// the stale one-entry state, and elevated exactly once for dns.
	st, ok, err := svc.State()
	if err != nil || !ok {
		t.Fatalf("state: ok=%v err=%v", ok, err)
	}
	if len(st.Servers) != 2 || st.Servers[0] != "192.168.29.1" {
		t.Fatalf("stale state not refreshed: %v", st.Servers)
	}
	if n := countPrefix(f.calls(), "elev: resolvectl dns"); n != 1 {
		t.Fatalf("expected exactly 1 dns elevation, got %d: %v", n, f.calls())
	}
}

func countPrefix(calls []string, prefix string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func TestParseLinkValues(t *testing.T) {
	cases := []struct {
		name, out, link string
		want            []string
	}{
		{"v4+v6", "Link 2 (enp0s1): 192.168.29.1 2405:201:a805:c923::c0a8:1d01\n", "enp0s1",
			[]string{"192.168.29.1", "2405:201:a805:c923::c0a8:1d01"}},
		{"empty", "Link 2 (enp0s1):\n", "enp0s1", nil},
		{"with port", "Link 2 (enp0s1): 127.0.0.1:5453\n", "enp0s1", []string{"127.0.0.1:5453"}},
		{"routing domains", "Link 2 (enp0s1): ~lan fritz.box\n", "enp0s1", []string{"~lan", "fritz.box"}},
		{"ignores other link", "Global: 1.1.1.1\nLink 3 (wlp0s0): 8.8.8.8\n", "enp0s1", nil},
	}
	for _, c := range cases {
		got := parseLinkValues(c.out, c.link)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDefaultLink(t *testing.T) {
	f, svc, _ := newFakeService(t)
	link, err := svc.DefaultLink(context.Background())
	if err != nil || link != "enp0s1" {
		t.Fatalf("got (%q, %v), want enp0s1", link, err)
	}

	f.run = func(name string, args ...string) ([]byte, error) {
		return []byte("default via 1.1.1.1 dev evil;rm -rf /\n"), nil
	}
	if _, err := svc.DefaultLink(context.Background()); err == nil {
		t.Fatal("interface name with shell metacharacters must be rejected")
	}

	f.run = func(string, ...string) ([]byte, error) { return []byte(""), nil }
	if _, err := svc.DefaultLink(context.Background()); err == nil {
		t.Fatal("no default route must error")
	}
}

func TestApplyPersistsStateBeforeElevating(t *testing.T) {
	f, svc, statePath := newFakeService(t)
	if err := svc.Apply(context.Background(), "127.0.0.1:5453"); err != nil {
		t.Fatal(err)
	}

	calls := f.calls()
	want := []string{
		"run: resolvectl status",
		"run: ip route show default",
		"run: resolvectl dns enp0s1",
		"run: resolvectl domain enp0s1",
		"elev: resolvectl dns enp0s1 127.0.0.1:5453",
		"elev: resolvectl domain enp0s1 ~.",
	}
	got := nonStateLines(calls)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("call sequence:\n got: %v\nwant: %v", got, want)
	}
	// Ordering proof: the state file existed at every elevation.
	if n := countLines(calls, "state: present"); n != 2 {
		t.Fatalf("state file missing during elevation (%d/2 proofs): %v", n, calls)
	}

	// Saved values must match what the machine reported.
	st, ok, err := svc.State()
	if err != nil || !ok {
		t.Fatalf("state not readable: ok=%v err=%v", ok, err)
	}
	if st.Link != "enp0s1" || st.Listen != "127.0.0.1:5453" || st.Backend != "resolved" {
		t.Fatalf("bad state: %+v", st)
	}
	if len(st.Servers) != 2 || st.Servers[0] != "192.168.29.1" {
		t.Fatalf("saved servers wrong: %v", st.Servers)
	}
	if len(st.Domains) != 0 {
		t.Fatalf("saved domains wrong: %v", st.Domains)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file missing: %v", err)
	}
}

func TestApplyDNSFailureDiscardsState(t *testing.T) {
	f, svc, statePath := newFakeService(t)
	f.elev = func(name string, args ...string) ([]byte, error) {
		return []byte("Not authorized to interact with org.freedesktop.resolve1"), errors.New("exit status 1")
	}
	err := svc.Apply(context.Background(), "127.0.0.1:5453")
	if err == nil || !strings.Contains(err.Error(), "Not authorized") {
		t.Fatalf("expected authorization error with pkexec output, got %v", err)
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("state must be discarded when nothing changed")
	}
}

func TestApplyDomainFailureRollsBack(t *testing.T) {
	f, svc, statePath := newFakeService(t)
	n := 0
	f.elev = func(name string, args ...string) ([]byte, error) {
		n++
		if n == 2 { // second elevation (domain) fails
			return []byte("boom"), errors.New("exit status 1")
		}
		return nil, nil
	}
	err := svc.Apply(context.Background(), "127.0.0.1:5453")
	if err == nil || !strings.Contains(err.Error(), "setting routing domain") {
		t.Fatalf("got %v", err)
	}
	calls := f.calls()
	// Rollback must restore the saved servers on the same link.
	want := "elev: resolvectl dns enp0s1 192.168.29.1 2405:201:a805:c923::c0a8:1d01"
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, want) {
		t.Fatalf("rollback call missing:\n%s", joined)
	}
	// Rollback succeeded -> state removed (machine back to normal).
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("state should be gone after successful rollback")
	}
}

func TestRestore(t *testing.T) {
	_, svc, statePath := newFakeService(t)
	// State as Apply would have written it (with saved values).
	if err := svc.saveState(State{
		Backend: "resolved", Link: "enp0s1", Listen: "127.0.0.1:5453",
		Servers: []string{"192.168.29.1"}, Domains: []string{"~lan"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("state must be removed after successful restore")
	}
}

func TestRestoreClearsWithEmptySavedValues(t *testing.T) {
	f, svc, statePath := newFakeService(t)
	if err := svc.saveState(State{Backend: "resolved", Link: "enp0s1"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.calls(), "\n")
	// A single empty argument must clear the list (resolvectl contract).
	if !strings.Contains(joined, "elev: resolvectl dns enp0s1 \n") {
		t.Fatalf("restore dns call with empty (clearing) value missing:\n%s", joined)
	}
	if !strings.Contains(joined, "elev: resolvectl domain enp0s1 \n") {
		t.Fatalf("restore domain call with empty (clearing) value missing:\n%s", joined)
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("state must be gone")
	}
}

// nonStateLines drops the ordering-proof lines from the call log.
func nonStateLines(calls []string) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		if strings.HasPrefix(c, "state:") {
			continue
		}
		out = append(out, c)
	}
	return out
}

func countLines(calls []string, want string) int {
	n := 0
	for _, c := range calls {
		if c == want {
			n++
		}
	}
	return n
}

func TestRestoreWithoutStateIsNoop(t *testing.T) {
	f, svc, _ := newFakeService(t)
	if err := svc.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls()) != 0 {
		t.Fatalf("expected no commands, got %v", f.calls())
	}
}

func TestRestoreFailureKeepsState(t *testing.T) {
	f, svc, statePath := newFakeService(t)
	if err := svc.saveState(State{Backend: "resolved", Link: "enp0s1", Servers: []string{"192.168.29.1"}}); err != nil {
		t.Fatal(err)
	}
	f.elev = func(string, ...string) ([]byte, error) { return []byte("denied"), errors.New("exit 1") }
	if err := svc.Restore(context.Background()); err == nil {
		t.Fatal("expected restore error")
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatal("state must survive a failed restore so repair can retry")
	}
}

func TestStateRejectsInvalidLink(t *testing.T) {
	_, svc, statePath := newFakeService(t)
	// Simulate a tampered/hand-edited state file with shell metacharacters.
	if err := os.WriteFile(statePath, []byte(`{"backend":"resolved","link":"enp0s1; rm -rf /"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := svc.State(); err == nil || ok {
		t.Fatalf("invalid link must be rejected: ok=%v err=%v", ok, err)
	}
}

func TestDetectAndStatus(t *testing.T) {
	_, svc, _ := newFakeService(t)
	if err := svc.Detect(context.Background()); err != nil {
		t.Fatalf("detect: %v", err)
	}

	st := svc.Status(context.Background())
	if !st.Supported || st.Link != "enp0s1" || st.Applied {
		t.Fatalf("bad status: %+v", st)
	}
	if len(st.CurrentServers) != 2 {
		t.Fatalf("current servers: %v", st.CurrentServers)
	}

	// Unsupported machine: missing resolvectl.
	svc.look = func(name string) error {
		if name == "resolvectl" {
			return errors.New("not found")
		}
		return nil
	}
	st = svc.Status(context.Background())
	if st.Supported || !strings.Contains(st.Message, "resolvectl") {
		t.Fatalf("expected unsupported with message, got %+v", st)
	}
}
