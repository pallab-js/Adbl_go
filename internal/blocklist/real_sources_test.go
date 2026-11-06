package blocklist

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ubl/adbl/internal/config"
)

// TestRealDefaultSources downloads the actual built-in sources and checks a
// known ad domain is blocked. Skipped with `go test -short`.
func TestRealDefaultSources(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	cfg, err := config.Open(filepath.Join(t.TempDir(), "cfg.json"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	m := NewManager(nil, cfg, store, nil) // nil -> DefaultSources
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	total := 0
	for _, st := range m.Statuses() {
		t.Logf("%-14s %6d domains  err=%q", st.ID, st.Domains, st.LastError)
		total += st.Domains
	}
	if total < 50_000 {
		t.Fatalf("only %d domains parsed from real sources — parser regression?", total)
	}
	for _, d := range []string{"ad.doubleclick.net", "pagead2.googlesyndication.com"} {
		if blocked, src := store.Check(d); !blocked {
			t.Errorf("%s not blocked by default sources (src=%q)", d, src)
		}
	}
	if blocked, _ := store.Check("example.com"); blocked {
		t.Error("example.com must not be blocked")
	}
}
