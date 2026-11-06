// Package blocklist parses community blocklist sources and answers
// "is this domain blocked?" against an immutable, hot-swappable snapshot.
package blocklist

import (
	"net"
	"strings"
)

// ParseLine parses one line from any supported source format:
// hosts ("0.0.0.0 ads.example.com"), plain domain lists, or Adblock
// ("||ads.example.com^"). Returns the normalized domain, or ok=false.
func ParseLine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
		return "", false
	}
	// Adblock exception and cosmetic rules are not supported: reject them
	// explicitly so trailing-comment trimming can't misread them as domains.
	if strings.HasPrefix(line, "@@") || strings.Contains(line, "##") || strings.Contains(line, "#@#") {
		return "", false
	}
	if strings.HasPrefix(line, "||") {
		rule := line[2:]
		if i := strings.IndexAny(rule, "^$"); i >= 0 {
			rule = rule[:i]
		}
		return ValidDomain(rule)
	}
	// Trailing comment (hosts style): "0.0.0.0 ads.example.com # note".
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", false
	}
	if net.ParseIP(fields[0]) != nil {
		if len(fields) < 2 {
			return "", false
		}
		return ValidDomain(fields[1])
	}
	return ValidDomain(fields[0])
}

// ValidDomain normalizes and validates a domain extracted from a source.
// Requires a dot so single-label entries like "localhost" can never be blocked.
func ValidDomain(s string) (string, bool) {
	s = Normalize(s)
	if len(s) < 3 || len(s) > 253 || !strings.Contains(s, ".") ||
		strings.HasPrefix(s, ".") {
		return "", false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '.' || r == '_' {
			continue
		}
		return "", false
	}
	// Labels may not start or end with a hyphen (also rejects "..").
	for _, label := range strings.Split(s, ".") {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
	}
	return s, true
}

// Normalize lowercases a domain and strips the trailing root dot.
func Normalize(domain string) string {
	return strings.ToLower(strings.TrimSuffix(domain, "."))
}
