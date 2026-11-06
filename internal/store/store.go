// Package store persists query events and serves the statistics the UI
// shows. Writes are batched off the DNS hot path; reads are plain SQL.
package store

import (
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ubl/adbl/internal/dnsengine"
)

const (
	flushInterval = 500 * time.Millisecond
	flushSize     = 200
	queueSize     = 4096
	rawRetention  = 7 * 24 * time.Hour   // detailed query log
	dayRetention  = 180 * 24 * time.Hour // daily rollups
	rollupEvery   = 6 * time.Hour
)

type Query struct {
	ID      int64  `json:"id"`
	TS      int64  `json:"ts"` // unix seconds
	Domain  string `json:"domain"`
	QType   string `json:"qtype"`
	Blocked bool   `json:"blocked"`
	Source  string `json:"source"`
}

type ListFilter struct {
	Blocked  *bool // nil = both
	Search   string
	Limit    int
	BeforeID int64 // cursor: rows with id < BeforeID (newest first)
}

type Bucket struct {
	TS      int64 `json:"ts"` // bucket start, unix seconds
	Total   int   `json:"total"`
	Blocked int   `json:"blocked"`
}

type TopEntry struct {
	Domain string `json:"domain"`
	Count  int    `json:"count"`
}

type Summary struct {
	Since   int64 `json:"since"`
	Total   int   `json:"total"`
	Blocked int   `json:"blocked"`
}

type DB struct {
	sql    *sql.DB
	ch     chan dnsengine.Event
	closed atomic.Bool
	stop   chan struct{}
	wg     sync.WaitGroup
}

// Open connects to path (creating schema as needed) and starts the batch writer.
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	dsn := path +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection keeps write batching and reads trivially correct;
	// batches are ≤200 rows, so read stalls are sub-millisecond.
	sqlDB.SetMaxOpenConns(1)

	d := &DB{sql: sqlDB, ch: make(chan dnsengine.Event, queueSize), stop: make(chan struct{})}
	if err := d.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	d.wg.Add(1)
	go d.loop()
	return d, nil
}

func (d *DB) migrate() error {
	_, err := d.sql.Exec(`
CREATE TABLE IF NOT EXISTS queries (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ts INTEGER NOT NULL,
	domain TEXT NOT NULL,
	qtype TEXT NOT NULL,
	blocked INTEGER NOT NULL,
	source TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_queries_ts ON queries(ts);
CREATE INDEX IF NOT EXISTS idx_queries_blocked_ts ON queries(blocked, ts);
CREATE TABLE IF NOT EXISTS daily_stats (
	date TEXT PRIMARY KEY,
	total INTEGER NOT NULL,
	blocked INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS meta (
	key TEXT PRIMARY KEY,
	val TEXT NOT NULL
);`)
	return err
}

// Log enqueues an event without ever blocking the DNS handler: if the queue
// is full (extreme load) the row is dropped rather than adding latency.
func (d *DB) Log(ev dnsengine.Event) {
	if d.closed.Load() {
		return
	}
	select {
	case d.ch <- ev:
	default:
	}
}

func (d *DB) loop() {
	defer d.wg.Done()
	tick := time.NewTicker(flushInterval)
	defer tick.Stop()
	batch := make([]dnsengine.Event, 0, flushSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := d.insert(batch); err != nil {
			log.Printf("store: insert batch: %v", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case ev := <-d.ch:
			batch = append(batch, ev)
			if len(batch) >= flushSize {
				flush()
			}
		case <-tick.C:
			flush()
		case <-d.stop:
			for { // drain what is already queued
				select {
				case ev := <-d.ch:
					batch = append(batch, ev)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (d *DB) insert(batch []dnsengine.Event) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(
		`INSERT INTO queries(ts, domain, qtype, blocked, source) VALUES(?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, ev := range batch {
		blocked := 0
		if ev.Blocked {
			blocked = 1
		}
		if _, err := stmt.Exec(ev.TS.Unix(), ev.Domain, ev.QType, blocked, ev.Source); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Close stops the writer, flushes the queue, and closes the database.
func (d *DB) Close() error {
	if !d.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(d.stop)
	d.wg.Wait()
	return d.sql.Close()
}

// StartMaintenance runs a rollup immediately, then every rollupEvery until
// ctx is done. Rollup aggregates completed days and prunes expired rows.
func (d *DB) StartMaintenance(ctx context.Context) {
	run := func() {
		if err := d.Rollup(time.Now()); err != nil {
			log.Printf("store: rollup: %v", err)
		}
	}
	run()
	go func() {
		tick := time.NewTicker(rollupEvery)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				run()
			}
		}
	}()
}

// Rollup upserts daily aggregates for completed days, then applies retention.
// Aggregate and prune share one transaction so no day can lose its numbers.
func (d *DB) Rollup(now time.Time) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	midnight := StartOfDay(now).Unix()
	_, err = tx.Exec(`
INSERT INTO daily_stats(date, total, blocked)
SELECT date(ts, 'unixepoch', 'localtime'), COUNT(*), SUM(blocked)
FROM queries WHERE ts < ?
GROUP BY 1
ON CONFLICT(date) DO UPDATE SET total = excluded.total, blocked = excluded.blocked`,
		midnight)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM queries WHERE ts < ?`, now.Add(-rawRetention).Unix()); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM daily_stats WHERE date < ?`,
		now.Add(-dayRetention).Format("2006-01-02")); err != nil {
		return err
	}
	return tx.Commit()
}

// Summary counts queries at or after since.
func (d *DB) Summary(since time.Time) (Summary, error) {
	var s Summary
	err := d.sql.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(blocked), 0) FROM queries WHERE ts >= ?`,
		since.Unix()).Scan(&s.Total, &s.Blocked)
	s.Since = since.Unix()
	return s, err
}

// Series buckets queries since `since` into fixed-width buckets (≤24h
// ranges; gaps are filled by the caller).
func (d *DB) Series(since time.Time, bucket time.Duration) ([]Bucket, error) {
	secs := int64(bucket.Seconds())
	rows, err := d.sql.Query(`
SELECT (ts / ?) * ? AS b, COUNT(*), SUM(blocked)
FROM queries WHERE ts >= ? GROUP BY b ORDER BY b`, secs, secs, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBuckets(rows)
}

// DailySeries returns local-day buckets for the last `days` days (today
// included), merging completed days from daily_stats with today's raw rows.
func (d *DB) DailySeries(days int, now time.Time) ([]Bucket, error) {
	out := []Bucket{}
	rows, err := d.sql.Query(`
SELECT date, total, blocked FROM daily_stats WHERE date >= ? ORDER BY date`,
		now.AddDate(0, 0, -(days-1)).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var date string
		var b Bucket
		if err := rows.Scan(&date, &b.Total, &b.Blocked); err != nil {
			rows.Close()
			return nil, err
		}
		t, err := time.ParseInLocation("2006-01-02", date, now.Location())
		if err != nil {
			rows.Close()
			return nil, err
		}
		b.TS = t.Unix()
		out = append(out, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Today (not yet rolled up) comes from the raw log.
	today, err := d.Summary(StartOfDay(now))
	if err != nil {
		return nil, err
	}
	if today.Total > 0 || len(out) == 0 || out[len(out)-1].TS != StartOfDay(now).Unix() {
		out = append(out, Bucket{TS: StartOfDay(now).Unix(), Total: today.Total, Blocked: today.Blocked})
	}
	return out, nil
}

// TopBlocked returns the most-blocked domains since `since`.
func (d *DB) TopBlocked(since time.Time, n int) ([]TopEntry, error) {
	rows, err := d.sql.Query(`
SELECT domain, COUNT(*) AS c FROM queries
WHERE blocked = 1 AND ts >= ? GROUP BY domain ORDER BY c DESC, domain LIMIT ?`,
		since.Unix(), n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TopEntry
	for rows.Next() {
		var e TopEntry
		if err := rows.Scan(&e.Domain, &e.Count); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// List returns query rows newest-first with optional filters and cursor.
func (d *DB) List(f ListFilter) ([]Query, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	var (
		wh   []string
		args []any
	)
	if f.Blocked != nil {
		wh = append(wh, "blocked = ?")
		args = append(args, *f.Blocked)
	}
	if f.Search != "" {
		wh = append(wh, "domain LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escapeLike(f.Search)+"%")
	}
	if f.BeforeID > 0 {
		wh = append(wh, "id < ?")
		args = append(args, f.BeforeID)
	}
	q := `SELECT id, ts, domain, qtype, blocked, source FROM queries`
	if len(wh) > 0 {
		q += " WHERE " + strings.Join(wh, " AND ")
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, f.Limit)

	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Query
	for rows.Next() {
		var qq Query
		var blocked int
		if err := rows.Scan(&qq.ID, &qq.TS, &qq.Domain, &qq.QType, &blocked, &qq.Source); err != nil {
			return nil, err
		}
		qq.Blocked = blocked != 0
		out = append(out, qq)
	}
	return out, rows.Err()
}

// StartOfDay returns local midnight of t's calendar day.
func StartOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func scanBuckets(rows *sql.Rows) ([]Bucket, error) {
	var out []Bucket
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.TS, &b.Total, &b.Blocked); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// escapeLike neutralizes LIKE wildcards in user search text.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
