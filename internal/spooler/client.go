package spooler

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tspm-project/tsp-manager/internal/model"
)

type Client struct {
	Binary string
	Socket string
}

func (c Client) run(ctx context.Context, args ...string) (string, error) {
	binary := c.Binary
	if binary == "" {
		binary = "tsp"
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = replaceEnv(os.Environ(), "TS_SOCKET", c.Socket)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg != "" {
			return stdout.String(), fmt.Errorf("%s: %w", msg, err)
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}

func replaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			out = append(out, item)
		}
	}
	if value != "" {
		out = append(out, prefix+value)
	}
	return out
}

func (c Client) List(ctx context.Context) ([]model.Task, error) {
	out, err := c.run(ctx, "-l")
	if err != nil {
		return nil, err
	}
	return ParseTaskList(out)
}

func (c Client) Slots(ctx context.Context) (int, error) {
	out, err := c.run(ctx, "-S")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("could not parse tsp slot count from %q", strings.TrimSpace(out))
	}
	return n, nil
}

func (c Client) SetSlots(ctx context.Context, n int) error {
	if n < 1 {
		return errors.New("slot count must be at least 1")
	}
	_, err := c.run(ctx, "-S", strconv.Itoa(n))
	return err
}

func (c Client) Kill(ctx context.Context, id int) error {
	_, err := c.run(ctx, "-k", strconv.Itoa(id))
	return err
}

func (c Client) Remove(ctx context.Context, id int) error {
	_, err := c.run(ctx, "-r", strconv.Itoa(id))
	return err
}

func (c Client) ClearFinished(ctx context.Context) error {
	_, err := c.run(ctx, "-C")
	return err
}

func (c Client) Urgent(ctx context.Context, id int) error {
	_, err := c.run(ctx, "-u", strconv.Itoa(id))
	return err
}

func (c Client) Info(ctx context.Context, id int) (string, error) {
	return c.run(ctx, "-i", strconv.Itoa(id))
}

func (c Client) OutputPath(ctx context.Context, id int) (string, error) {
	out, err := c.run(ctx, "-o", strconv.Itoa(id))
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(out)
	if path == "" || path == "(file)" || path == "-" {
		return "", nil
	}
	return path, nil
}

// Output returns the stored output of a job via the daemon protocol (tsp -c).
// For a running task, tsp -c streams until completion and may hit the context
// deadline; in that case any output already captured before timeout is returned.
func (c Client) Output(ctx context.Context, id int) (string, error) {
	out, err := c.run(ctx, "-c", strconv.Itoa(id))
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) && out != "" {
		return out, nil
	}
	return out, err
}

func (c Client) SubmitShell(ctx context.Context, command string) (int, error) {
	out, err := c.run(ctx, "sh", "-c", command)
	if err != nil {
		return -1, err
	}
	return parseJobID(out)
}

func (c Client) SubmitArgs(ctx context.Context, args []string) (int, error) {
	if len(args) == 0 {
		return -1, errors.New("no command supplied")
	}
	// A single argument is treated as a shell command string (e.g. -- "cmd | tee output.log"),
	// unless it directly references an existing executable/file path on disk (which may contain spaces).
	if len(args) == 1 {
		if st, err := os.Stat(args[0]); err == nil && !st.IsDir() {
			return c.SubmitShell(ctx, shellQuote(args[0]))
		}
		return c.SubmitShell(ctx, args[0])
	}
	// Multiple arguments are individually quoted and executed as one command.
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return c.SubmitShell(ctx, strings.Join(quoted, " "))
}

func parseJobID(output string) (int, error) {
	for _, f := range strings.Fields(output) {
		if id, err := strconv.Atoi(strings.TrimSpace(f)); err == nil {
			return id, nil
		}
	}
	return -1, fmt.Errorf("could not parse task ID from tsp output %q", strings.TrimSpace(output))
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// NormalizeOutputLines splits raw log output into lines, collapses carriage-return
// progress updates, drops a single trailing newline, and limits to maxLines.
func NormalizeOutputLines(raw string, dropLeadingPartial bool, maxLines int64) []string {
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if dropLeadingPartial && len(lines) > 1 {
		lines = lines[1:]
	}
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if maxLines > 0 && int64(len(lines)) > maxLines {
		lines = lines[len(lines)-int(maxLines):]
	}
	for i, line := range lines {
		if strings.ContainsRune(line, '\r') {
			lines[i] = resolveCarriageReturn(line)
		}
	}
	return lines
}

func resolveCarriageReturn(line string) string {
	parts := strings.Split(line, "\r")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return ""
}

func ReadTail(path string, maxLines, maxBytes int64) ([]string, error) {
	if maxLines < 1 {
		maxLines = 30
	}
	if maxBytes < 1024 {
		maxBytes = 64 * 1024
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() == 0 {
		return nil, nil
	}

	// Detect gzip-compressed output files created with `tsp -g`.
	var magic [2]byte
	n, _ := f.ReadAt(magic[:], 0)
	if n == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		const maxDecompress = 8 * 1024 * 1024
		data, err := io.ReadAll(io.LimitReader(gz, maxDecompress))
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, err
		}
		if int64(len(data)) > maxBytes {
			data = data[len(data)-int(maxBytes):]
			return NormalizeOutputLines(string(data), true, maxLines), nil
		}
		return NormalizeOutputLines(string(data), false, maxLines), nil
	}

	start := st.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return nil, err
	}
	return NormalizeOutputLines(string(data), start > 0, maxLines), nil
}
