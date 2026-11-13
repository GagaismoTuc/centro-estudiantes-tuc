package httpapi

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestIPKey(t *testing.T) {
	s := &server{trusted: []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("fd00::/8"),
	}}
	for _, tc := range []struct {
		name string
		peer string
		xff  []string // one entry per X-Forwarded-For header
		want string
	}{
		{"untrusted peer without header", "203.0.113.7:1", nil, "ip:203.0.113.7"},
		{"untrusted peer: header ignored", "203.0.113.7:1", []string{"198.18.0.1"}, "ip:203.0.113.7"},
		{"trusted peer without header is the client", "10.0.0.1:1", nil, "ip:10.0.0.1"},
		{"IPv4-mapped trusted peer", "[::ffff:10.0.0.1]:1", []string{"198.18.0.1"}, "ip:198.18.0.1"},
		{"IPv4-mapped hop", "10.0.0.1:1", []string{"::ffff:198.18.0.1"}, "ip:198.18.0.1"},
		{"multiple headers are one list", "10.0.0.1:1", []string{"192.0.2.1", "198.18.0.1, 10.0.0.2"}, "ip:198.18.0.1"},
		{"trusted middle hops are skipped", "10.0.0.1:1", []string{"192.0.2.1, 198.18.0.1, 10.0.0.2, 10.0.0.3"}, "ip:198.18.0.1"},
		{"all hops trusted: left-most hop", "10.0.0.1:1", []string{"10.0.0.5, 10.0.0.2"}, "ip:10.0.0.5"},
		{"trailing comma", "10.0.0.1:1", []string{"198.18.0.1,"}, "ip:198.18.0.1"},
		{"garbage left of the client", "10.0.0.1:1", []string{"garbage, 198.18.0.1"}, "ip:198.18.0.1"},
		{"garbage before any untrusted hop: never the proxy", "10.0.0.1:1", []string{"198.18.0.1, garbage, 10.0.0.2"}, "ip:unparsable"},
		{"IPv4 hop with port", "10.0.0.1:1", []string{"198.18.0.1:5678"}, "ip:198.18.0.1"},
		{"IPv6 hop with port", "10.0.0.1:1", []string{"[2001:db8::1]:443"}, "ip:2001:db8::/64"},
		{"IPv6 hop with zone", "10.0.0.1:1", []string{"fe80::1%eth0"}, "ip:fe80::/64"},
		{"IPv6 peer with untrusted header", "[2001:db8:1:2::9]:1", []string{"198.18.0.1"}, "ip:2001:db8:1:2::/64"},
		{"trusted IPv6 peer", "[fd00::1]:1", []string{"2001:db8:5::7"}, "ip:2001:db8:5::/64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.peer
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := s.ipKey(r); got != tc.want {
				t.Fatalf("ipKey = %q, want %q", got, tc.want)
			}
		})
	}
}
