package blocklist

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ubl/adbl/internal/config"
)

// newTestManager serves two fake sources: s1 (hosts format), s2 (adblock format).
func newTestManager(t *testing.T, s1Status int, s1Body string) (*Manager, *Store, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/s1":
			w.WriteHeader(s1Status)
			w.Write([]byte(s1Body))
		case "/s2":
			w.Write([]byte("||tracker.example.com^\n"))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)

	cfg, err := config.Open(filepath.Join(t.TempDir(), "cfg.json"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	sources := []Source{
		{ID: "s1", Name: "Source One", URL: srv.URL + "/s1"},
		{ID: "s2", Name: "Source Two", URL: srv.URL + "/s2"},
	}
	return NewManager(sources, cfg, store, nil), store, srv
}

func TestRefreshSwapsSnapshot(t *testing.T) {
	m, store, _ := newTestManager(t, 200, "0.0.0.0 ads.example.com\n")
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if blocked, src := store.Check("ads.example.com"); !blocked || src != "s1" {
		t.Fatalf("got (%v, %q), want (true, s1)", blocked, src)
	}
	if blocked, src := store.Check("tracker.example.com"); !blocked || src != "s2" {
		t.Fatalf("got (%v, %q), want (true, s2)", blocked, src)
	}
	st := m.Statuses()
	if len(st) != 2 || st[0].Domains == 0 || st[0].LastError != "" {
		t.Fatalf("bad statuses: %+v", st)
	}
}

func TestRefreshFailureKeepsLastGoodData(t *testing.T) {
	m, store, _ := newTestManager(t, 200, "0.0.0.0 ads.example.com\n")
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Server now fails: snapshot must keep blocking, error must surface.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	m.sources[0].URL = srv.URL + "/s1"
	m.sources[1].URL = srv.URL + "/s2"

	if err := m.Refresh(context.Background()); err == nil {
		t.Fatal("expected error when all sources fail")
	}
	if blocked, _ := store.Check("ads.example.com"); !blocked {
		t.Fatal("last-good data must survive a failed refresh")
	}
	for _, st := range m.Statuses() {
		if st.LastError == "" {
			t.Fatalf("expected LastError set: %+v", st)
		}
	}
}

func TestDisableSourceRemovesItsDomainsWithoutRefetch(t *testing.T) {
	m, store, _ := newTestManager(t, 200, "0.0.0.0 ads.example.com\n")
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Point sources at a dead server: toggling must NOT need the network.
	m.sources[0].URL = "http://127.0.0.1:1/s1"
	m.sources[1].URL = "http://127.0.0.1:1/s2"

	if err := m.SetEnabled("s1", false); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := store.Check("ads.example.com"); blocked {
		t.Fatal("disabled source's domains must be dropped")
	}
	if blocked, src := store.Check("tracker.example.com"); !blocked || src != "s2" {
		t.Fatalf("other source must stay: (%v, %q)", blocked, src)
	}
	if err := m.SetEnabled("s1", true); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := store.Check("ads.example.com"); !blocked {
		t.Fatal("re-enabled source must restore domains from cache")
	}
}

func TestManualRules(t *testing.T) {
	m, store, _ := newTestManager(t, 200, "0.0.0.0 ads.example.com\n")
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Allow wins over a blocked source.
	if err := m.AddRule(true, "ads.example.com"); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := store.Check("ads.example.com"); blocked {
		t.Fatal("manual allow must override blocklist")
	}

	// Manual block works for domains no source lists.
	if err := m.AddRule(false, "custom-block.example.org"); err != nil {
		t.Fatal(err)
	}
	if blocked, src := store.Check("custom-block.example.org"); !blocked || src != "manual" {
		t.Fatalf("got (%v, %q), want (true, manual)", blocked, src)
	}

	// Remove restores the blocklist decision / clears the manual block.
	if err := m.RemoveRule(true, "ads.example.com"); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := store.Check("ads.example.com"); !blocked {
		t.Fatal("removing allow must restore blocklist block")
	}
	if err := m.RemoveRule(false, "custom-block.example.org"); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := store.Check("custom-block.example.org"); blocked {
		t.Fatal("removed manual block must not block")
	}
}

func TestAddRuleRejectsInvalidDomain(t *testing.T) {
	m, _, _ := newTestManager(t, 200, "x")
	for _, bad := range []string{"", "localhost", "not a domain", "ex ample.com", "-bad.com."} {
		if err := m.AddRule(true, bad); err == nil {
			t.Errorf("AddRule(%q) should fail", bad)
		}
	}
}

func TestConcurrentRefreshAndCheck(t *testing.T) {
	m, store, _ := newTestManager(t, 200, "0.0.0.0 ads.example.com\n")
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); m.Refresh(context.Background()) }()
	go func() { defer wg.Done(); m.Refresh(context.Background()) }() // skipped, one runs
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			store.Check("ads.example.com")
			m.Statuses()
		}
	}()
	wg.Wait()
}
