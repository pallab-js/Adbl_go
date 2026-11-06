package main

import (
	"context"
	"sync"
	"time"

	"github.com/ubl/adbl/internal/dnsengine"
	"github.com/ubl/adbl/internal/service"
	"github.com/ubl/adbl/internal/store"
)

// queryBatch collects engine events and emits them to the frontend every
// 500ms (the `dns:batch` live ticker). Each event is also handed to the
// store for persistence — one fan-out point, no double work in the engine.
type queryBatch struct {
	db  *store.DB
	mu  sync.Mutex
	buf []dnsengine.Event
}

func newQueryBatch(db *store.DB) *queryBatch { return &queryBatch{db: db} }

// onQuery is wired as the engine's callback; never blocks the DNS handler.
func (q *queryBatch) onQuery(ev dnsengine.Event) {
	q.db.Log(ev)
	q.mu.Lock()
	q.buf = append(q.buf, ev)
	q.mu.Unlock()
}

// Query is the frontend-facing row shape for the live ticker.
type Query struct {
	TS      int64  `json:"ts"` // unix seconds
	Domain  string `json:"domain"`
	QType   string `json:"qtype"`
	Blocked bool   `json:"blocked"`
	Source  string `json:"source"`
}

// run drains the buffer every 500ms and emits dns:batch until ctx ends.
func (q *queryBatch) run(ctx context.Context, emit func(name string, data any)) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			q.mu.Lock()
			buf := q.buf
			q.buf = nil
			q.mu.Unlock()
			if len(buf) == 0 {
				continue
			}
			out := make([]Query, len(buf))
			for i, ev := range buf {
				out[i] = Query{
					TS: ev.TS.Unix(), Domain: ev.Domain, QType: ev.QType,
					Blocked: ev.Blocked, Source: ev.Source,
				}
			}
			emit("dns:batch", out)
		}
	}
}

// statsTicker emits stats:tick every second with dashboard aggregates only
// (never raw rows — see UI-UX §4 chart rules).
func statsTicker(ctx context.Context, stats *service.StatsService, sources func() int,
	emit func(name string, data any)) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			today, err := stats.Summary("today")
			if err != nil {
				continue
			}
			day24, err := stats.Summary("24h")
			if err != nil {
				continue
			}
			series, err := stats.Series("1h", "1m")
			if err != nil {
				series = nil
			}
			emit("stats:tick", service.StatsTick{
				Today: today, Day24: day24, Series60: series, Sources: sources(),
			})
		}
	}
}
