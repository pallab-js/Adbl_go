package blocklist

import "testing"

func TestParseLine(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"0.0.0.0 ads.example.com", "ads.example.com", true},
		{"0.0.0.0 ads.example.com # comment", "ads.example.com", true},
		{"127.0.0.1 tracker.example.org", "tracker.example.org", true},
		{"::1 dual.example.com", "dual.example.com", true},
		{"ads.example.net", "ads.example.net", true},    // plain list
		{"ADS.Example.NET", "ads.example.net", true},    // normalized
		{"||ads.example.com^", "ads.example.com", true}, // adblock
		{"||ads.example.com^$third-party", "ads.example.com", true},
		{"# comment line", "", false},
		{"! adblock comment", "", false},
		{"", "", false},
		{"   ", "", false},
		{"@@||allowed.example.com^", "", false},       // exception rule
		{"example.com##.ad-banner", "", false},        // cosmetic rule
		{"example.com#@#.ad", "", false},              // cosmetic exception
		{"0.0.0.0", "", false},                        // no domain
		{"localhost", "", false},                      // single label
		{"0.0.0.0 localhost", "", false},              // single label target
		{"||example.com/ads/path^", "", false},        // path rule
		{"||*.example.com^", "", false},               // wildcard unsupported
		{"0.0.0.0 example.com.", "example.com", true}, // trailing dot stripped
	}
	for _, c := range cases {
		got, ok := ParseLine(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseLine(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
