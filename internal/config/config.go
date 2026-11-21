package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Queue struct {
	Name   string `json:"name"`
	Socket string `json:"socket"`
}

type Config struct {
	TSPBin                 string  `json:"tsp_bin"`
	RefreshIntervalSeconds int     `json:"refresh_interval_seconds"`
	AutoDiscover           bool    `json:"auto_discover"`
	Queues                 []Queue `json:"queues"`
}

func DefaultPath() string {
	if p := os.Getenv("TSPM_CONFIG"); p != "" {
		return p
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "tspm", "config.json")
}

func Default() Config {
	return Config{
		TSPBin:                 "tsp",
		RefreshIntervalSeconds: 2,
		AutoDiscover:           true,
		Queues:                 []Queue{},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	if strings.TrimSpace(cfg.TSPBin) == "" {
		cfg.TSPBin = "tsp"
	}
	if cfg.RefreshIntervalSeconds < 1 {
		cfg.RefreshIntervalSeconds = 2
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("empty config path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	success = true
	return nil
}
