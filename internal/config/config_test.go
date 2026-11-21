package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSaveConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.json")

	// Missing config returns defaults without error.
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TSPBin != "tsp" || cfg.RefreshIntervalSeconds != 2 || !cfg.AutoDiscover {
		t.Fatalf("unexpected default config: %#v", cfg)
	}

	cfg.Queues = []Queue{{Name: "gpu0", Socket: "/tmp/tsp-gpu0.sock"}}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Queues) != 1 || loaded.Queues[0].Name != "gpu0" {
		t.Fatalf("unexpected loaded config: %#v", loaded)
	}

	// Ensure no leftover .tmp files exist.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only config.json, found %d files", len(entries))
	}
}
