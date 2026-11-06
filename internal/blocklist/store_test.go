package blocklist

import "testing"

func TestSnapshotCheck(t *testing.T) {
	snap := NewSnapshot(map[string]string{
		"doubleclick.net": "stevenblack",
		"ads.example.com": "hagezi",
	}, []string{"ads.example.com"})

	cases := []struct {
		domain  string
		blocked bool
		source  string
	}{
		{"doubleclick.net", true, "stevenblack"},     // exact
		{"ad.doubleclick.net", true, "stevenblack"},  // subdomain
		{"a.b.doubleclick.net", true, "stevenblack"}, // deep subdomain
		{"DOUBLECLICK.NET.", true, "stevenblack"},    // normalized input
		{"notdoubleclick.net", false, ""},            // no partial-label match
		{"example.org", false, ""},                   // unknown
		{"com", false, ""},                           // TLD alone never queried this way
		{"ads.example.com", false, ""},               // manual allow wins over block
		{"www.ads.example.com", false, ""},           // allow covers subdomains
	}
	for _, c := range cases {
		blocked, source := snap.Check(c.domain)
		if blocked != c.blocked || source != c.source {
			t.Errorf("Check(%q) = (%v, %q), want (%v, %q)",
				c.domain, blocked, source, c.blocked, c.source)
		}
	}
}

func TestSnapshotIsImmutableAndSwappable(t *testing.T) {
	st := NewStore()
	if blocked, _ := st.Check("ads.example.com"); blocked {
		t.Fatal("fresh store should not block")
	}
	st.Set(NewSnapshot(map[string]string{"ads.example.com": "s1"}, nil))
	if blocked, src := st.Check("ads.example.com"); !blocked || src != "s1" {
		t.Fatalf("after swap: got (%v, %q)", blocked, src)
	}
	st.Set(NewSnapshot(nil, nil)) // refresh clears
	if blocked, _ := st.Check("ads.example.com"); blocked {
		t.Fatal("cleared store should not block")
	}
}
