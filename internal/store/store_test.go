package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ubl/adbl/internal/dnsengine"
)

func open(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d, path
}

// seed inserts a raw row directly with an exact timestamp.
func seed(t *testing.T, d *DB, ts time.Time, domain string, blocked bool) {
	t.Helper()
	b := 0
	if blocked {
		b = 1
	}
	if _, err := d.sql.Exec(`INSERT INTO queries(ts, domain, qtype, blocked, source)
		VALUES(?, ?, 'A', ?, 'test')`, ts.Unix(), domain, b); err != nil {
		t.Fatal(err)
	}
}

func ev(domain string, blocked bool) dnsengine.Event {
	return dnsengine.Event{TS: time.Now(), Domain: domain, QType: "A", Blocked: blocked, Source: "s"}
}

func TestLogFlushesOnCloseAndPersists(t *testing.T) {
	d, path := open(t)
	d.Log(ev("a.ads.com", true))
	d.Log(ev("b.example.com", false))
	d.Log(ev("c.ads.com", true))
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	d2, err := Open(path) // reopen = restart
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	rows, err := d2.List(ListFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows after reopen = %d, want 3", len(rows))
	}
	if rows[0].Domain != "c.ads.com" || !rows[0].Blocked || rows[0].Source != "s" {
		t.Fatalf("unexpected newest row: %+v", rows[0])
	}
}

func TestBatchFlushBySizeWithoutClose(t *testing.T) {
	d, _ := open(t)
	for i := 0; i < 250; i++ {
		d.Log(ev("batch.example.com", false))
	}
	// 200-row threshold triggers the first flush inside the loop; the 50-row
	// tail lands on the 500ms tick. Wait for all 250.
	deadline := time.Now().Add(3 * time.Second)
	for {
		s, err := d.Summary(time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if s.Total == 250 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("batch never fully flushed: total=%d", s.Total)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSummarySeriesTopAndList(t *testing.T) {
	d, _ := open(t)
	now := time.Now()
	// Seed oldest-first so row ids (insertion order) match chronological
	// order, as they do in production where events arrive in time order.
	seed(t, d, now.Add(-90*time.Minute), "old.ads.com", true) // outside 1h window
	seed(t, d, now.Add(-30*time.Minute), "ok.example.com", false)
	seed(t, d, now.Add(-20*time.Minute), "a.ads.com", true)
	seed(t, d, now.Add(-10*time.Minute), "a.ads.com", true)

	s, err := d.Summary(now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 3 || s.Blocked != 2 {
		t.Fatalf("summary = %+v, want 3/2", s)
	}

	buckets, err := d.Series(now.Add(-time.Hour), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var tot, blk int
	for _, b := range buckets {
		tot += b.Total
		blk += b.Blocked
	}
	if tot != 3 || blk != 2 {
		t.Fatalf("series totals = %d/%d, want 3/2", tot, blk)
	}

	top, err := d.TopBlocked(now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || top[0].Domain != "a.ads.com" || top[0].Count != 2 {
		t.Fatalf("top = %+v", top)
	}

	// Blocked filter.
	yes := true
	rows, err := d.List(ListFilter{Blocked: &yes, Limit: 10})
	if err != nil || len(rows) != 3 {
		t.Fatalf("blocked filter: %d rows, err=%v", len(rows), err)
	}
	// Cursor paging: newest first, second page has the rest.
	page1, _ := d.List(ListFilter{Limit: 2})
	if len(page1) != 2 || page1[0].TS < page1[1].TS {
		t.Fatalf("page1 = %+v", page1)
	}
	page2, _ := d.List(ListFilter{Limit: 10, BeforeID: page1[1].ID})
	if len(page2) != 2 {
		t.Fatalf("page2 = %+v (cursor broken)", page2)
	}
}

func TestListSearchEscapesLikeWildcards(t *testing.T) {
	d, _ := open(t)
	now := time.Now()
	seed(t, d, now, "50%_off.example", true)
	seed(t, d, now, "50-off.example", true)

	rows, err := d.List(ListFilter{Search: "50%_off", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Domain != "50%_off.example" {
		t.Fatalf("search must match literally, got %+v", rows)
	}
}

func TestRollupRetentionAndDailySeries(t *testing.T) {
	d, _ := open(t)
	now := time.Now()
	today := StartOfDay(now)

	seed(t, d, now.Add(-1*time.Hour), "today.ads.com", true)
	seed(t, d, now.Add(-1*time.Hour), "today.ok.com", false)
	for i := 0; i < 4; i++ {
		seed(t, d, today.AddDate(0, 0, -3).Add(time.Duration(i)*time.Hour), "three.ads.com", i%2 == 0)
	}
	for i := 0; i < 5; i++ { // older than 7d raw retention
		seed(t, d, today.AddDate(0, 0, -10).Add(time.Duration(i)*time.Hour), "ten.ads.com", i < 3)
	}
	seed(t, d, today.AddDate(0, 0, -200), "ancient.ads.com", true) // beyond 180d

	if err := d.Rollup(now); err != nil {
		t.Fatal(err)
	}

	// Raw retention: 10-day and 200-day rows gone, 3-day rows kept.
	var oldRaw int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM queries WHERE ts < ?`,
		now.Add(-rawRetention).Unix()).Scan(&oldRaw); err != nil {
		t.Fatal(err)
	}
	if oldRaw != 0 {
		t.Fatalf("%d rows survived raw retention", oldRaw)
	}

	// daily_stats holds completed days; ancient day pruned at 180d.
	days, err := d.DailySeries(7, now)
	if err != nil {
		t.Fatal(err)
	}
	byTS := map[int64]Bucket{}
	for _, b := range days {
		byTS[b.TS] = b
	}
	three := today.AddDate(0, 0, -3).Unix()
	if b, ok := byTS[three]; !ok || b.Total != 4 || b.Blocked != 2 {
		t.Fatalf("3-day bucket missing/wrong: %+v", byTS)
	}
	if b, ok := byTS[today.Unix()]; !ok || b.Total != 2 || b.Blocked != 1 {
		t.Fatalf("today bucket missing/wrong: %+v", byTS)
	}
	if _, ok := byTS[today.AddDate(0, 0, -10).Unix()]; ok {
		t.Fatal("10-day bucket must come only from rollup (raw pruned), and be outside 7d window")
	}
	if _, ok := byTS[today.AddDate(0, 0, -200).Unix()]; ok {
		t.Fatal("200-day bucket must be pruned at 180d")
	}
}

func TestWALAndStartOfDay(t *testing.T) {
	d, _ := open(t)
	var mode string
	if err := d.sql.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}

	now := time.Now()
	sod := StartOfDay(now)
	if sod.Hour() != 0 || sod.Day() != now.Day() || !sod.Equal(StartOfDay(sod.Add(5*time.Hour))) {
		t.Fatalf("StartOfDay(%v) = %v", now, sod)
	}
}
