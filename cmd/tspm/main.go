package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tspm-project/tsp-manager/internal/config"
	"github.com/tspm-project/tsp-manager/internal/manager"
	"github.com/tspm-project/tsp-manager/internal/model"
	"github.com/tspm-project/tsp-manager/internal/ui"
)

var version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tspm:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfgPath := config.DefaultPath()
	tspBinOverride := ""
	for len(args) > 0 {
		if args[0] == "--config" {
			if len(args) < 2 {
				return errors.New("--config requires a file path")
			}
			cfgPath, args = args[1], args[2:]
			continue
		}
		if strings.HasPrefix(args[0], "--config=") {
			cfgPath, args = strings.TrimPrefix(args[0], "--config="), args[1:]
			continue
		}
		if args[0] == "--tsp-bin" {
			if len(args) < 2 {
				return errors.New("--tsp-bin requires an executable path")
			}
			tspBinOverride, args = args[1], args[2:]
			continue
		}
		if strings.HasPrefix(args[0], "--tsp-bin=") {
			tspBinOverride, args = strings.TrimPrefix(args[0], "--tsp-bin="), args[1:]
			continue
		}
		break
	}

	// Handle help and version before loading config so a malformed config
	// never blocks inspecting CLI usage or version.
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-V":
			fmt.Printf("tspm %s\n", version)
			return nil
		case "help", "--help", "-h":
			printHelp(cfgPath)
			return nil
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if tspBinOverride != "" {
		cfg.TSPBin = tspBinOverride
	}
	m := manager.New(cfg, cfgPath)

	if len(args) == 0 {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return ui.New(m, version).Run(ctx)
	}

	switch args[0] {
	case "config":
		if len(args) == 2 && args[1] == "path" {
			fmt.Println(cfgPath)
			return nil
		}
		if len(args) == 2 && args[1] == "init" {
			if _, err := os.Stat(cfgPath); err == nil {
				return fmt.Errorf("config already exists: %s", cfgPath)
			}
			if err := config.Save(cfgPath, cfg); err != nil {
				return err
			}
			fmt.Println("Created", cfgPath)
			return nil
		}
		return errors.New("usage: tspm config path|init")
	case "doctor":
		if len(args) > 1 {
			return errors.New("usage: tspm doctor")
		}
		return doctor(m, cfgPath)
	case "queues":
		if len(args) > 1 {
			return errors.New("usage: tspm queues")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.Refresh(ctx); err != nil {
			return err
		}
		printQueues(m.Snapshot())
		return nil
	case "status":
		fs := flag.NewFlagSet("status", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		_ = fs.Bool("json", true, "output JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() > 0 {
			return errors.New("usage: tspm status [--json]")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.Refresh(ctx); err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(m.Snapshot())
	case "queue":
		if len(args) < 2 {
			return errors.New("usage: tspm queue add <name> <socket> | remove <name>")
		}
		switch args[1] {
		case "add":
			if len(args) != 4 {
				return errors.New("usage: tspm queue add <name> <socket>")
			}
			if err := m.AddQueue(args[2], args[3]); err != nil {
				return err
			}
			fmt.Printf("Registered queue %q -> %s\n", args[2], filepath.Clean(args[3]))
			return nil
		case "remove":
			if len(args) != 3 {
				return errors.New("usage: tspm queue remove <name>")
			}
			if err := m.RemoveQueue(args[2]); err != nil {
				return err
			}
			fmt.Printf("Removed registration for %q (the tsp server was not stopped)\n", args[2])
			return nil
		default:
			return errors.New("usage: tspm queue add <name> <socket> | remove <name>")
		}
	case "submit":
		fs := flag.NewFlagSet("submit", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		queue := fs.String("q", "", "queue name")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *queue == "" {
			return errors.New("submit requires -q <queue>")
		}
		cmdArgs := fs.Args()
		if len(cmdArgs) > 0 && cmdArgs[0] == "--" {
			cmdArgs = cmdArgs[1:]
		}
		if len(cmdArgs) == 0 {
			return errors.New("submit requires a command after --")
		}
		if _, err := m.BinaryAvailable(); err != nil {
			return err
		}
		m.DiscoverOnly()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		id, err := m.SubmitArgs(ctx, *queue, cmdArgs)
		if err != nil {
			return err
		}
		fmt.Printf("Submitted task #%d to queue %s\n", id, *queue)
		return nil
	default:
		printHelp(cfgPath)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func doctor(m *manager.Manager, cfgPath string) error {
	fmt.Println("TSP Manager doctor")
	fmt.Println("Config:", cfgPath)
	binary, err := m.BinaryAvailable()
	if err != nil {
		fmt.Println("[FAIL] tsp binary:", err)
		return err
	}
	fmt.Println("[ OK ] tsp binary:", binary)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Refresh(ctx); err != nil {
		return err
	}
	queues := m.Snapshot()
	fmt.Printf("Discovered/registered queues: %d\n", len(queues))
	for _, q := range queues {
		if q.Active {
			slots := "?"
			if q.Slots >= 1 {
				slots = fmt.Sprintf("%d", q.Slots)
			}
			fmt.Printf("[ OK ] %-16s %s (running=%d pending=%d slots=%s)\n", q.Name, q.Socket, q.RunningCount(), q.PendingCount(), slots)
		} else {
			fmt.Printf("[WARN] %-16s %s (%s)\n", q.Name, q.Socket, q.Error)
		}
	}
	if len(queues) == 0 {
		fmt.Println("[INFO] No tsp queues found. Start one with tsp <command> or register it with 'tspm queue add'.")
	}
	return nil
}

func printQueues(queues []model.Queue) {
	fmt.Printf("%-18s %-8s %-10s %-9s %-9s %s\n", "QUEUE", "STATUS", "RUNNING", "PENDING", "FINISHED", "SOCKET")
	for _, q := range queues {
		status := "offline"
		if q.Active {
			status = "active"
		}
		running := fmt.Sprintf("%d/?", q.RunningCount())
		if q.Slots >= 1 {
			running = fmt.Sprintf("%d/%d", q.RunningCount(), q.Slots)
		}
		fmt.Printf("%-18s %-8s %-10s %-9d %-9d %s\n", q.Name, status, running, q.PendingCount(), q.FinishedCount(), q.Socket)
		if q.Error != "" {
			fmt.Println("  !", q.Error)
		}
	}
}

func printHelp(cfgPath string) {
	fmt.Printf(`tspm %s — multi-queue manager for Task Spooler

Usage:
  tspm [global options]                 Open interactive TUI
  tspm queues                           List discovered queues
  tspm status [--json]                  Print queue/task state as JSON
  tspm doctor                           Check tsp installation and queues
  tspm queue add <name> <socket>        Register a custom socket
  tspm queue remove <name>              Remove a queue registration only
  tspm submit -q <name> -- <command>   Submit a command to a queue
  tspm config path                      Print config path
  tspm config init                      Create default config
  tspm version                          Print version

Global options (must appear before the command):
  --config <path>                       Use an alternate JSON config
  --tsp-bin <path>                      Use a non-default tsp executable

TUI keys:
  ↑/↓       Navigate queue/task list       Tab/←/→   Switch pane / open details
  Enter     Open tasks / task details      i         Task details
  l         View live output               Esc/←     Close details / logs / help
  a         Submit shell command           n         Register queue
  d         Remove queue registration      k         Send SIGTERM to task
  r         Remove task from queue         C         Clear finished tasks
  u         Move task forward              s         Change queue slots
  R         Refresh immediately            ?         Show help
  q/Esc     Quit                           Ctrl-C    Quit immediately

Config: %s
`, version, filepath.Clean(cfgPath))
}
