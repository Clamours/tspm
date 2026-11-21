package discovery

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"io"
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
	byKey := make(map[string]model.Queue)
	ordered := make([]string, 0)
	add := func(q model.Queue) {
		q.Socket = filepath.Clean(q.Socket)
		if q.Socket == "." || q.Socket == "" {
			return
		}
		key := canonicalSocketPath(q.Socket)
		if existing, ok := byKey[key]; ok {
			if existing.Source == "auto" && q.Source != "auto" {
				byKey[key] = q
			}
			return
		}
		byKey[key] = q
		ordered = append(ordered, key)
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

		// Sort active paths before iterating so auto-discovery is deterministic.
		sortedActive := make([]string, 0, len(activePaths))
		for _, origPath := range activePaths {
			sortedActive = append(sortedActive, origPath)
		}
		sort.Strings(sortedActive)

		for _, path := range sortedActive {
			if !knownSocketName.MatchString(path) || transientSocketName.MatchString(filepath.Base(path)) {
				continue
			}
			st, err := os.Stat(path)
			if err != nil || st.Mode()&os.ModeSocket == 0 || !ownedByCurrentUser(st) {
				continue
			}
			name := queueNameFromPath(path)
			add(model.Queue{Name: name, Socket: path, Source: "auto"})
		}
	}

	queues := make([]model.Queue, 0, len(ordered))
	for _, key := range ordered {
		q := byKey[key]
		_, listed := activePaths[key]
		st, statErr := os.Stat(q.Socket)
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
		sort.SliceStable(auto, func(i, j int) bool {
			if auto[i].Name != auto[j].Name {
				return auto[i].Name < auto[j].Name
			}
			return auto[i].Socket < auto[j].Socket
		})
		queues = append(configured, auto...)
	}

	disambiguateQueueNames(queues)
	return queues
}

func disambiguateQueueNames(queues []model.Queue) {
	used := make(map[string]int, len(queues))
	for i := range queues {
		name := queues[i].Name
		if used[name] == 0 {
			used[name] = 1
			continue
		}
		// Keep configured/explicit names untouched if possible; suffix later collisions.
		n := used[name] + 1
		candidate := fmt.Sprintf("%s-%d", name, n)
		for used[candidate] > 0 {
			n++
			candidate = fmt.Sprintf("%s-%d", name, n)
		}
		used[name] = n
		used[candidate] = 1
		queues[i].Name = candidate
	}
}

func canonicalSocketPath(path string) string {
	clean := filepath.Clean(path)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(resolved)
	}
	return clean
}

func socketPathExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode()&os.ModeSocket != 0
}

func activeUnixListeners() map[string]string {
	f, err := os.Open("/proc/net/unix")
	if err != nil {
		return make(map[string]string)
	}
	defer f.Close()
	return parseProcNetUnix(f)
}

func parseProcNetUnix(r io.Reader) map[string]string {
	result := make(map[string]string)
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
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
		// Type 0001 = SOCK_STREAM, St 01 = SS_UNCONNECTED, Flags & 0x00010000 = __SO_ACCEPTCON (listening).
		if len(fields) < 8 || fields[4] != "0001" || fields[5] != "01" {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 64)
		if err != nil || flags&0x00010000 == 0 {
			continue
		}
		path := strings.TrimSpace(strings.Join(fields[7:], " "))
		if path == "" || strings.HasPrefix(path, "@") {
			continue
		}
		clean := filepath.Clean(path)
		key := canonicalSocketPath(clean)
		if _, exists := result[key]; !exists {
			result[key] = clean
		}
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
		h := fnv.New32a()
		_, _ = h.Write([]byte(path))
		return fmt.Sprintf("queue-%08x", h.Sum32())
	}
	return base
}
