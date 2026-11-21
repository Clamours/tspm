package manager

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/tspm-project/tsp-manager/internal/config"
	"github.com/tspm-project/tsp-manager/internal/discovery"
	"github.com/tspm-project/tsp-manager/internal/model"
	"github.com/tspm-project/tsp-manager/internal/spooler"
)

type Manager struct {
	mu      sync.RWMutex
	cfg     config.Config
	cfgPath string
	queues  []model.Queue
	lastErr error
	binary  string
}

func New(cfg config.Config, cfgPath string) *Manager {
	return &Manager{cfg: cfg, cfgPath: cfgPath, binary: cfg.TSPBin}
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

func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	queues := discovery.Discover(cfg)

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
			} else {
				q.Slots = 1
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

func (m *Manager) queueClient(name string) (spooler.Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, q := range m.queues {
		if q.Name == name {
			if !q.Active {
				return spooler.Client{}, fmt.Errorf("queue %q is not active: %s", name, q.Error)
			}
			return spooler.Client{Binary: m.cfg.TSPBin, Socket: q.Socket}, nil
		}
	}
	return spooler.Client{}, fmt.Errorf("queue %q not found", name)
}

func (m *Manager) AddQueue(name, socket string) error {
	name, socket = strings.TrimSpace(name), strings.TrimSpace(socket)
	if name == "" || socket == "" {
		return errors.New("queue name and socket path are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, q := range m.cfg.Queues {
		if q.Name == name {
			return fmt.Errorf("queue name %q already exists", name)
		}
		if q.Socket == socket {
			return fmt.Errorf("socket %q is already registered", socket)
		}
	}
	m.cfg.Queues = append(m.cfg.Queues, config.Queue{Name: name, Socket: socket})
	if err := config.Save(m.cfgPath, m.cfg); err != nil {
		m.cfg.Queues = m.cfg.Queues[:len(m.cfg.Queues)-1]
		return err
	}
	return nil
}

func (m *Manager) RemoveQueue(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, q := range m.cfg.Queues {
		if q.Name == name {
			previous := append([]config.Queue(nil), m.cfg.Queues...)
			m.cfg.Queues = append(m.cfg.Queues[:i], m.cfg.Queues[i+1:]...)
			if err := config.Save(m.cfgPath, m.cfg); err != nil {
				m.cfg.Queues = previous
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("queue %q is not explicitly registered; auto-discovered queues cannot be removed from config", name)
}

func (m *Manager) SubmitShell(ctx context.Context, queueName, command string) (int, error) {
	c, err := m.queueClient(queueName)
	if err != nil {
		return -1, err
	}
	return c.SubmitShell(ctx, command)
}

func (m *Manager) SubmitArgs(ctx context.Context, queueName string, args []string) (int, error) {
	c, err := m.queueClient(queueName)
	if err != nil {
		return -1, err
	}
	return c.SubmitArgs(ctx, args)
}

func (m *Manager) Kill(ctx context.Context, queueName string, id int) error {
	c, err := m.queueClient(queueName)
	if err != nil {
		return err
	}
	return c.Kill(ctx, id)
}
func (m *Manager) Remove(ctx context.Context, queueName string, id int) error {
	c, err := m.queueClient(queueName)
	if err != nil {
		return err
	}
	return c.Remove(ctx, id)
}
func (m *Manager) ClearFinished(ctx context.Context, queueName string) error {
	c, err := m.queueClient(queueName)
	if err != nil {
		return err
	}
	return c.ClearFinished(ctx)
}
func (m *Manager) Urgent(ctx context.Context, queueName string, id int) error {
	c, err := m.queueClient(queueName)
	if err != nil {
		return err
	}
	return c.Urgent(ctx, id)
}
func (m *Manager) SetSlots(ctx context.Context, queueName string, n int) error {
	c, err := m.queueClient(queueName)
	if err != nil {
		return err
	}
	return c.SetSlots(ctx, n)
}
func (m *Manager) Info(ctx context.Context, queueName string, id int) (string, error) {
	c, err := m.queueClient(queueName)
	if err != nil {
		return "", err
	}
	return c.Info(ctx, id)
}
func (m *Manager) OutputPath(ctx context.Context, queueName string, id int) (string, error) {
	c, err := m.queueClient(queueName)
	if err != nil {
		return "", err
	}
	return c.OutputPath(ctx, id)
}
func (m *Manager) TaskOutput(ctx context.Context, queueName string, id int) (string, error) {
	c, err := m.queueClient(queueName)
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
