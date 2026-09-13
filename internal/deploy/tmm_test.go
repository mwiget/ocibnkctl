package deploy

import "testing"

func TestTMMNotReady(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		workers int
		want    string
	}{
		{"healthy single worker", "1 1 1", 1, ""},
		{"healthy three workers", "3 3 3", 3, ""},
		// The false-OK shape: deploy cne's rollout restart replaced the running
		// TMM with a pod that stayed Pending, so numberAvailable was absent.
		{"restarted pod pending", "1 1 ", 1, "0 of 1 TMM pod(s) available"},
		{"mid rollout", "2 1 2", 2, "1 of 2 TMM pod(s) updated"},
		{"no labelled worker", "0 0 0", 1, "f5-tmm scheduled on 0 of 1 app=f5-tmm worker(s)"},
		{"fewer pods than workers", "1 1 1", 2, "f5-tmm scheduled on 1 of 2 app=f5-tmm worker(s)"},
		{"empty status", "", 1, "f5-tmm scheduled on 0 of 1 app=f5-tmm worker(s)"},
		{"workers unset defaults to one", "1 1 1", 0, ""},
	}
	for _, c := range cases {
		if got := tmmNotReady(parseTMMRollout(c.raw), c.workers); got != c.want {
			t.Errorf("%s: tmmNotReady(%q, %d) = %q, want %q", c.name, c.raw, c.workers, got, c.want)
		}
	}
}

// Malformed counts read as 0 so they can never pass as ready.
func TestParseTMMRollout_Malformed(t *testing.T) {
	got := parseTMMRollout("x 1 y extra")
	if got != (tmmRollout{Desired: 0, Updated: 1, Available: 0}) {
		t.Errorf("parseTMMRollout = %+v", got)
	}
}

func TestDescribeTMMPods(t *testing.T) {
	raw := "f5-tmm-cx5ms\tPending\t0/2 nodes are available: 1 Insufficient memory.\n" +
		"f5-tmm-l2f4l\tRunning\t\n" +
		"\n"
	want := "f5-tmm-cx5ms Pending: 0/2 nodes are available: 1 Insufficient memory.; f5-tmm-l2f4l Running"
	if got := describeTMMPods(raw); got != want {
		t.Errorf("describeTMMPods = %q, want %q", got, want)
	}
	if got := describeTMMPods(""); got != "" {
		t.Errorf("describeTMMPods(\"\") = %q, want empty", got)
	}
}
