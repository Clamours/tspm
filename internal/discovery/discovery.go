package discovery

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tspm-project/tsp-manager/internal/config"
	"github.com/tspm-project/tsp-manager/internal/model"
)

var knownSocketName = regexp.MustCompile(`(?i)(socket-ts|(^|[/_-])tsp?([._/-]|$)|task.?spooler)`)

var transientSocketName = regexp.MustCompile(`(?i)^ts-out\.`)

func Discover(cfg config.Config) []model.Queue {
	activePaths := activeUnixListeners()
	byPath := make(map[string]model.Queue)
	ordered := make([]string, 0)
	add := func(q model.Queue) {
		q.Socket = filepath.Clean(q.Socket)
		if q.Socket == "." || q.Socket == "" {
			return
		}
		if existing, ok := byPath[q.Socket]; ok {
			if existing.Source == "auto" && q.Source != "auto" {
				byPath[q.Socket] = q
			}
			return
		}
		byPath[q.Socket] = q
		ordered = append(ordered, q.Socket)
	}

	for _, q := range cfg.Queues {
		if strings.TrimSpace(q.Name) == "" || strings.TrimSpace(q.Socket) == "" {
			continue
		}
		add(model.Queue{Name: q.Name, Socket: q.Socket, Source: "config"})
	}

	if cfg.AutoDiscover {
		if envSocket := os.Getenv("TS_SOCKET"); envSocket != "" && socketPathExists(envSocket) {
			add(model.Queue{Name: "environment", Socket: envSocket, Source: "TS_SOCKET"})
		}
		tmp := os.Getenv("TMPDIR")
		if tmp == "" {
			tmp = os.TempDir()
		}
		defaultSocket := filepath.Join(tmp, "socket-ts."+strconv.Itoa(os.Getuid()))
		if socketPathExists(defaultSocket) {
			add(model.Queue{Name: "default", Socket: defaultSocket, Source: "default"})
		}

		for path := range activePaths {
			if !knownSocketName.MatchString(path) || transientSocketName.MatchString(filepath.Base(path)) {
				continue
			}
			name := queueNameFromPath(path)
			add(model.Queue{Name: name, Socket: path, Source: "auto"})
		}
	}

	queues := make([]model.Queue, 0, len(ordered))
	for _, path := range ordered {
		q := byPath[path]
		_, listed := activePaths[path]
		st, statErr := os.Stat(path)
		if statErr == nil && st.Mode()&os.ModeSocket == 0 {
			q.Error = "path exists but is not a Unix socket"
		} else if statErr != nil {
			q.Error = "socket path not found"
		} else if !listed {
			q.Error = "socket file exists but no active listener was found"
		} else {
			q.Active = true
		}
		queues = append(queues, q)
	}

	// Keep configured queues first, then sort discovered queues for stable UI order.
	if len(queues) > 1 {
		configured := make([]model.Queue, 0, len(queues))
		auto := make([]model.Queue, 0, len(queues))
		for _, q := range queues {
			if q.Source == "config" || q.Source == "TS_SOCKET" || q.Source == "default" {
				configured = append(configured, q)
			} else {
				auto = append(auto, q)
			}
		}
		sort.SliceStable(auto, func(i, j int) bool { return auto[i].Name < auto[j].Name })
		queues = append(configured, auto...)
	}
	return queues
}

func socketPathExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode()&os.ModeSocket != 0
}

func activeUnixListeners() map[string]struct{} {
	result := make(map[string]struct{})
	f, err := os.Open("/proc/net/unix")
	if err != nil {
		return result
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	first := true
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		if first {
			first = false
			continue
		}
		fields := strings.Fields(line)
		// /proc/net/unix: Num RefCount Protocol Flags Type St Inode Path
		if len(fields) < 8 || fields[4] != "0001" || fields[5] != "01" {
			continue
		}
		path := strings.TrimSpace(strings.Join(fields[7:], " "))
		if path == "" || strings.HasPrefix(path, "@") {
			continue
		}
		result[filepath.Clean(path)] = struct{}{}
	}
	return result
}

func queueNameFromPath(path string) string {
	base := filepath.Base(path)
	if strings.HasPrefix(base, "socket-ts.") {
		return "default"
	}
	base = strings.TrimSuffix(base, ".sock")
	base = strings.TrimSuffix(base, ".socket")
	base = strings.TrimPrefix(base, "tsp-")
	base = strings.TrimPrefix(base, "tsp_")
	base = strings.TrimPrefix(base, "ts-")
	base = strings.TrimPrefix(base, "ts_")
	base = strings.TrimPrefix(base, "socket-")
	if base == "" {
		return fmt.Sprintf("queue-%x", len(path))
	}
	return base
}
