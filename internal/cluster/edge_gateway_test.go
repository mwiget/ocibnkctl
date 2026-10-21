package cluster

import "testing"

func TestIPv4Gateway(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"172.20.0.1\n", "172.20.0.1"},
		// Docker Desktop's dual-stack default: IPv4 first or second
		{"172.20.0.1\nfd95:edf0:ab3d:3::1\n", "172.20.0.1"},
		{"fd95:edf0:ab3d:3::1\n172.20.0.1\n", "172.20.0.1"},
		{"fd95:edf0:ab3d:3::1\n", ""},
		{"", ""},
	} {
		if got := ipv4Gateway(c.in); got != c.want {
			t.Errorf("ipv4Gateway(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
