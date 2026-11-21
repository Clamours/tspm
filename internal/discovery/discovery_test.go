package discovery

import "testing"

func TestKnownSocketName(t *testing.T) {
	matches := []string{
		"/tmp/socket-ts.1006",
		"/tmp/tsp-gpu0.sock",
		"/tmp/my_tsp/socket",
		"/tmp/task-spooler.sock",
		"/tmp/taskspooler.sock",
		"/tmp/ts-score-oeb",
		"/tmp/ts-scoring",
		"/tmp/ts_out.sock",
		"/run/user/1006/ts.gpu",
	}
	for _, path := range matches {
		if !knownSocketName.MatchString(path) {
			t.Errorf("expected match: %s", path)
		}
	}

	nonMatches := []string{
		"/tmp/mysql.sock",
		"/tmp/dbus-session",
		"/tmp/outside.socket",
		"/tmp/its-a-trap.sock",
	}
	for _, path := range nonMatches {
		if knownSocketName.MatchString(path) {
			t.Errorf("unexpected match: %s", path)
		}
	}
}

func TestTransientSocketName(t *testing.T) {
	if !transientSocketName.MatchString("ts-out.UDJkFC") {
		t.Error("expected ts-out.* to be treated as transient")
	}
	if transientSocketName.MatchString("ts-score-oeb") {
		t.Error("ts-score-oeb should not be transient")
	}
}

func TestQueueNameFromPath(t *testing.T) {
	cases := map[string]string{
		"/tmp/socket-ts.1006":     "default",
		"/tmp/tsp-gpu0.sock":      "gpu0",
		"/tmp/tsp_render.socket":  "render",
		"/tmp/ts-score-oeb":       "score-oeb",
		"/tmp/ts-scoring":         "scoring",
		"/tmp/socket-worker.sock": "worker",
	}
	for path, want := range cases {
		if got := queueNameFromPath(path); got != want {
			t.Errorf("queueNameFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}
