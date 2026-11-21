package manager

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Clamours/tspm/internal/config"
	"github.com/Clamours/tspm/internal/model"
)

func TestAddAndRemoveQueueNormalizationAndConcurrency(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Default()
	cfg.AutoDiscover = false
	m := New(cfg, cfgPath)

	if err := m.AddQueue("gpu0", "/tmp//tsp-gpu0.sock"); err != nil {
		t.Fatal(err)
	}
	// Duplicate socket with uncleaned path must be rejected.
	if err := m.AddQueue("gpu0-dup", "/tmp/tsp-gpu0.sock"); err == nil {
		t.Fatal("expected duplicate socket error")
	}

	// Run Refresh concurrently with AddQueue/RemoveQueue to verify race safety.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_ = m.Refresh(context.Background())
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			_ = m.AddQueue("tmpq", "/tmp/tsp-tmpq.sock")
			_ = m.RemoveQueue("/tmp/tsp-tmpq.sock")
		}
	}()
	wg.Wait()

	if err := m.RemoveQueue("gpu0"); err != nil {
		t.Fatalf("RemoveQueue by name failed: %v", err)
	}
}

func TestQueueClientResolvesBySocketFirst(t *testing.T) {
	m := New(config.Default(), filepath.Join(t.TempDir(), "config.json"))
	m.queues = []model.Queue{
		{Name: "dup", Socket: "/tmp/first.sock", Active: true},
		{Name: "dup", Socket: "/tmp/second.sock", Active: true},
	}

	c, err := m.queueClient("/tmp/second.sock")
	if err != nil {
		t.Fatal(err)
	}
	if c.Socket != "/tmp/second.sock" {
		t.Fatalf("expected /tmp/second.sock, got %q", c.Socket)
	}
}

func TestRefreshReturnsErrorWhenBinaryMissing(t *testing.T) {
	cfg := config.Default()
	cfg.AutoDiscover = false
	cfg.TSPBin = "definitely-nonexistent-tsp-binary-xyz"
	m := New(cfg, filepath.Join(t.TempDir(), "config.json"))

	if err := m.Refresh(context.Background()); err == nil {
		t.Fatal("expected error when tsp binary does not exist")
	}
}
