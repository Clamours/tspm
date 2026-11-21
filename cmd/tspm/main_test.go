package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCLIHelpVersionWorkWithBrokenConfig(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(broken, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--config", broken, "--version"}); err != nil {
		t.Fatalf("--version failed with broken config: %v", err)
	}
	if err := run([]string{"--config", broken, "--help"}); err != nil {
		t.Fatalf("--help failed with broken config: %v", err)
	}
}

func TestCLIMissingGlobalFlagValues(t *testing.T) {
	if err := run([]string{"--config"}); err == nil {
		t.Fatal("expected error for missing --config value")
	}
	if err := run([]string{"--tsp-bin"}); err == nil {
		t.Fatal("expected error for missing --tsp-bin value")
	}
}

func TestCLIStatusRejectsUnknownFlag(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := run([]string{"--config", cfgPath, "status", "--bogus"}); err == nil {
		t.Fatal("expected error for unknown status flag")
	}
}
