package manager

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Clamours/tspm/internal/config"
	"github.com/Clamours/tspm/internal/discovery"
	"github.com/Clamours/tspm/internal/model"
	"github.com/Clamours/tspm/internal/spooler"
)

type Manager struct {
	mu      sync.RWMutex
	cfg     config.Config
	cfgPath string
	queues  []model.Queue
}

func New(cfg config.Config, cfgPath string) *Manager {
	return &Manager{cfg: cfg, cfgPath: cfgPath}
}

func (m *Manager) ConfigPath() string { return m.cfgPath }

func (m *Manager) Config() config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	copyCfg := m.cfg
	copyCfg.Queues = append([]config.Queue(nil), m.cfg.Queues...)
	return copyCfg
}

func (m *Manager) Snapshot() []model.Queue {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.Queue, len(m.queues))
	copy(out, m.queues)
	for i := range out {
		out[i].Tasks = append([]model.Task(nil), out[i].Tasks...)
	}
	return out
}

// DiscoverOnly refreshes the list of discovered/configured queues without
// invoking `tsp -l` or `tsp -S` on every queue.
func (m *Manager) DiscoverOnly() {
	cfg := m.Config()
	queues := discovery.Discover(cfg)
	m.mu.Lock()
	m.queues = queues
	m.mu.Unlock()
}

func (m *Manager) Refresh(ctx context.Context) error {
	cfg := m.Config()
	queues := discovery.Discover(cfg)

	_, binErr := exec.LookPath(cfg.TSPBin)
	if binErr != nil {
		for i := range queues {
			if queues[i].Active {
				queues[i].Active = false
				queues[i].Error = fmt.Sprintf("cannot find %q in PATH", cfg.TSPBin)
			}
		}
		m.mu.Lock()
		m.queues = queues
		m.mu.Unlock()
		return fmt.Errorf("cannot find %q in PATH: %w", cfg.TSPBin, binErr)
	}

	var wg sync.WaitGroup
	for i := range queues {
		if !queues[i].Active {
			continue
		}
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			q := &queues[index]
			client := spooler.Client{Binary: cfg.TSPBin, Socket: q.Socket}
			callCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
			defer cancel()
			tasks, err := client.List(callCtx)
			if err != nil {
				q.Error = err.Error()
				q.Active = false
				return
			}
			now := time.Now()
			for j := range tasks {
				tasks[j].UpdatedAt = now
			}
			q.Tasks = tasks
			slots, err := client.Slots(callCtx)
			if err == nil {
				q.Slots = slots
			}
			q.UpdatedAt = time.Now()
		}(i)
	}
	wg.Wait()
	m.mu.Lock()
	m.queues = queues
	m.mu.Unlock()
	return nil
}

// queueClient resolves a queue by exact socket path first, then by queue name.
func (m *Manager) queueClient(selector string) (spooler.Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cleanSelector := filepath.Clean(selector)
	for _, q := range m.queues {
		if q.Socket == selector || q.Socket == cleanSelector {
			if !q.Active {
				return spooler.Client{}, fmt.Errorf("queue %q is not active: %s", q.Name, q.Error)
			}
			return spooler.Client{Binary: m.cfg.TSPBin, Socket: q.Socket}, nil
		}
	}
	for _, q := range m.queues {
		if q.Name == selector {
			if !q.Active {
				return spooler.Client{}, fmt.Errorf("queue %q is not active: %s", q.Name, q.Error)
			}
			return spooler.Client{Binary: m.cfg.TSPBin, Socket: q.Socket}, nil
		}
	}
	return spooler.Client{}, fmt.Errorf("queue %q not found", selector)
}

func (m *Manager) AddQueue(name, socket string) error {
	name, socket = strings.TrimSpace(name), strings.TrimSpace(socket)
	if name == "" || socket == "" {
		return errors.New("queue name and socket path are required")
	}
	socket = filepath.Clean(socket)
	if socket == "." {
		return errors.New("invalid socket path")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, q := range m.cfg.Queues {
		if q.Name == name {
			return fmt.Errorf("queue name %q already exists", name)
		}
		if filepath.Clean(q.Socket) == socket {
			return fmt.Errorf("socket %q is already registered", socket)
		}
	}
	next := append(append([]config.Queue(nil), m.cfg.Queues...), config.Queue{Name: name, Socket: socket})
	cfgCopy := m.cfg
	cfgCopy.Queues = next
	if err := config.Save(m.cfgPath, cfgCopy); err != nil {
		return err
	}
	m.cfg.Queues = next
	return nil
}

func (m *Manager) RemoveQueue(selector string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cleanSelector := filepath.Clean(selector)
	idx := -1
	for i, q := range m.cfg.Queues {
		if q.Socket == selector || filepath.Clean(q.Socket) == cleanSelector {
			idx = i
			break
		}
	}
	if idx < 0 {
		for i, q := range m.cfg.Queues {
			if q.Name == selector {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		return fmt.Errorf("queue %q is not explicitly registered; auto-discovered queues cannot be removed from config", selector)
	}
	next := make([]config.Queue, 0, len(m.cfg.Queues)-1)
	next = append(next, m.cfg.Queues[:idx]...)
	next = append(next, m.cfg.Queues[idx+1:]...)
	cfgCopy := m.cfg
	cfgCopy.Queues = next
	if err := config.Save(m.cfgPath, cfgCopy); err != nil {
		return err
	}
	m.cfg.Queues = next
	return nil
}

func (m *Manager) SubmitShell(ctx context.Context, queueSelector, command string) (int, error) {
	c, err := m.queueClient(queueSelector)
	if err != nil {
		return -1, err
	}
	return c.SubmitShell(ctx, command)
}

func (m *Manager) SubmitArgs(ctx context.Context, queueSelector string, args []string) (int, error) {
	c, err := m.queueClient(queueSelector)
	if err != nil {
		return -1, err
	}
	return c.SubmitArgs(ctx, args)
}

func (m *Manager) Kill(ctx context.Context, queueSelector string, id int) error {
	c, err := m.queueClient(queueSelector)
	if err != nil {
		return err
	}
	return c.Kill(ctx, id)
}
func (m *Manager) Remove(ctx context.Context, queueSelector string, id int) error {
	c, err := m.queueClient(queueSelector)
	if err != nil {
		return err
	}
	return c.Remove(ctx, id)
}
func (m *Manager) ClearFinished(ctx context.Context, queueSelector string) error {
	c, err := m.queueClient(queueSelector)
	if err != nil {
		return err
	}
	return c.ClearFinished(ctx)
}
func (m *Manager) Urgent(ctx context.Context, queueSelector string, id int) error {
	c, err := m.queueClient(queueSelector)
	if err != nil {
		return err
	}
	return c.Urgent(ctx, id)
}
func (m *Manager) SetSlots(ctx context.Context, queueSelector string, n int) error {
	c, err := m.queueClient(queueSelector)
	if err != nil {
		return err
	}
	return c.SetSlots(ctx, n)
}
func (m *Manager) Info(ctx context.Context, queueSelector string, id int) (string, error) {
	c, err := m.queueClient(queueSelector)
	if err != nil {
		return "", err
	}
	return c.Info(ctx, id)
}
func (m *Manager) OutputPath(ctx context.Context, queueSelector string, id int) (string, error) {
	c, err := m.queueClient(queueSelector)
	if err != nil {
		return "", err
	}
	return c.OutputPath(ctx, id)
}
func (m *Manager) TaskOutput(ctx context.Context, queueSelector string, id int) (string, error) {
	c, err := m.queueClient(queueSelector)
	if err != nil {
		return "", err
	}
	return c.Output(ctx, id)
}

func (m *Manager) BinaryAvailable() (string, error) {
	cfg := m.Config()
	path, err := exec.LookPath(cfg.TSPBin)
	if err != nil {
		return "", fmt.Errorf("cannot find %q in PATH: %w", cfg.TSPBin, err)
	}
	return path, nil
}
