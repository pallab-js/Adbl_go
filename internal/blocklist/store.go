package blocklist

import (
	"strings"
	"sync/atomic"
)

// Classifier answers block decisions; implemented by *Snapshot.
type Classifier interface {
	Check(domain string) (blocked bool, source string)
}

// Snapshot is an immutable view of all block + allow rules. It is replaced
// wholesale on blocklist refresh (atomic swap), never mutated in place.
type Snapshot struct {
	blocked map[string]string // domain -> source id
	allowed map[string]struct{}
}

// NewSnapshot builds a snapshot. blocked maps a domain to the source that
// provided it; allowed holds manual allow rules (which always win).
func NewSnapshot(blocked map[string]string, allowed []string) *Snapshot {
	s := &Snapshot{blocked: blocked, allowed: make(map[string]struct{}, len(allowed))}
	for _, d := range allowed {
		if d = Normalize(d); d != "" {
			s.allowed[d] = struct{}{}
		}
	}
	return s
}

// Check reports whether the domain is blocked. Manual allow rules take
// precedence over any block source.
func (s *Snapshot) Check(domain string) (bool, string) {
	domain = Normalize(domain)
	if _, ok := suffixLookup(domain, s.allowed); ok {
		return false, ""
	}
	if src, ok := suffixLookup(domain, s.blocked); ok {
		return true, src
	}
	return false, ""
}

// suffixLookup walks "a.b.c.com" -> "b.c.com" -> "c.com" -> "com".
func suffixLookup[T any](domain string, m map[string]T) (T, bool) {
	for {
		if v, ok := m[domain]; ok {
			return v, true
		}
		i := strings.IndexByte(domain, '.')
		if i < 0 {
			var zero T
			return zero, false
		}
		domain = domain[i+1:]
	}
}

// Store holds the current snapshot and swaps it atomically on refresh.
type Store struct {
	snap atomic.Pointer[Snapshot]
}

func NewStore() *Store {
	st := &Store{}
	st.Set(NewSnapshot(nil, nil))
	return st
}

func (st *Store) Set(s *Snapshot) { st.snap.Store(s) }

func (st *Store) Check(domain string) (bool, string) {
	return st.snap.Load().Check(domain)
}
