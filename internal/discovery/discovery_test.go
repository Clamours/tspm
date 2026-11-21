package discovery

import (
	"strings"
	"testing"

	"github.com/tspm-project/tsp-manager/internal/model"
)

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

	a := queueNameFromPath("/tmp/tsp-.sock")
	b := queueNameFromPath("/var/tsp-.sock")
	if a == b || !strings.HasPrefix(a, "queue-") {
		t.Errorf("expected distinct hashed fallback names, got %q and %q", a, b)
	}
}

func TestParseProcNetUnixRequiresListeningFlag(t *testing.T) {
	sample := strings.Join([]string{
		"Num       RefCount Protocol Flags    Type St Inode Path",
		"0000000000000001: 00000002 00000000 00010000 0001 01 12345 /tmp/tsp-listening.sock",
		"0000000000000002: 00000002 00000000 00000000 0001 01 12346 /tmp/tsp-not-listening.sock",
		"0000000000000003: 00000002 00000000 00010000 0002 01 12347 /tmp/tsp-dgram.sock",
		"0000000000000004: 00000002 00000000 00010000 0001 03 12348 /tmp/tsp-connected.sock",
		"0000000000000005: 00000002 00000000 00010000 0001 01 12349 @abstract-tsp",
	}, "\n")

	got := parseProcNetUnix(strings.NewReader(sample))
	if len(got) != 1 {
		t.Fatalf("expected 1 active listener, got %d: %#v", len(got), got)
	}
	if _, ok := got[canonicalSocketPath("/tmp/tsp-listening.sock")]; !ok {
		t.Fatalf("expected listening socket in %#v", got)
	}
}

func TestDisambiguateQueueNames(t *testing.T) {
	queues := []model.Queue{
		{Name: "gpu0", Socket: "/tmp/tsp-gpu0.sock"},
		{Name: "gpu0", Socket: "/var/tmp/ts-gpu0"},
		{Name: "gpu0", Socket: "/run/tsp-gpu0.sock"},
	}
	disambiguateQueueNames(queues)
	if queues[0].Name != "gpu0" || queues[1].Name != "gpu0-2" || queues[2].Name != "gpu0-3" {
		t.Fatalf("unexpected disambiguated names: %#v", queues)
	}
}
