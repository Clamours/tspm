package spooler

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseModernTaskList(t *testing.T) {
	header := "ID   State      Output               E-Level  Times(r/u/s)   Command [run=1/1]"
	rows := []string{
		fmt.Sprintf("%-4s %-10s %-20s %-8s %-14s %s", "1", "running", "/tmp/ts-out.abcd", "", "", "python train.py --epochs  3"),
		fmt.Sprintf("%-4s %-10s %-20s %-8s %-14s %s", "2", "queued", "(file)", "", "", "python eval.py"),
		fmt.Sprintf("%-4s %-10s %-20s %-8s %-14s %s", "0", "finished", "/tmp/ts-out.old", "0", "10.04/0.00/0.00", "python prepare.py"),
		fmt.Sprintf("%-4s %-10s %-20s %-8s %-14s %s", "3", "skipped", "(file)", "-1", "", "python after.py"),
		fmt.Sprintf("%-4s %-10s %-20s %-8s %-14s %s", "4", "finished", "/tmp/a b/ts-out.1", "1", "0.50/0.01/0.00", "echo  two  spaces"),
	}
	input := header + "\n" + strings.Join(rows, "\n")
	tasks, err := ParseTaskList(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 5 {
		t.Fatalf("got %d tasks", len(tasks))
	}
	if tasks[0].ID != 1 || tasks[0].State != "running" {
		t.Fatalf("unexpected task 0: %#v", tasks[0])
	}
	if tasks[0].Command != "python train.py --epochs  3" {
		t.Fatalf("unexpected running command (spacing should be preserved): %q", tasks[0].Command)
	}
	if tasks[1].Output != "" {
		t.Fatalf("queued output should be empty, got %q", tasks[1].Output)
	}
	if tasks[1].Command != "python eval.py" {
		t.Fatalf("unexpected queued command: %q", tasks[1].Command)
	}
	if tasks[2].ExitCode != "0" {
		t.Fatalf("expected exit code 0, got %q", tasks[2].ExitCode)
	}
	if tasks[3].State != "skipped" || tasks[3].ExitCode != "-1" || tasks[3].Command != "python after.py" {
		t.Fatalf("unexpected skipped task: %#v", tasks[3])
	}
	if tasks[4].Output != "/tmp/a b/ts-out.1" || tasks[4].ExitCode != "1" || tasks[4].Command != "echo  two  spaces" {
		t.Fatalf("unexpected space-path task: %#v", tasks[4])
	}
}

func TestParseLegacyTaskList(t *testing.T) {
	header := "ID   State      E-Level  Times    [run=0/1] Command &>Output"
	rows := []string{
		fmt.Sprintf("%-4s %-10s %-8s %-8s %-9s %s", "0", "finished", "0", "0.93", "[run=0/1]", "[user]echo hello &>/tmp/ts-out.abc"),
		fmt.Sprintf("%-4s %-10s %-8s %-8s %-9s %s", "1", "running", "?", "?", "[run=0/1]", "python train.py"),
	}
	input := header + "\n" + strings.Join(rows, "\n")
	tasks, err := ParseTaskList(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks", len(tasks))
	}
	if tasks[0].ID != 0 || tasks[0].ExitCode != "0" {
		t.Fatalf("unexpected task: %#v", tasks[0])
	}
	if tasks[0].Command == "" {
		t.Fatalf("expected command to be parsed")
	}
}

func TestReadTailProgressGzipAndSingleLongLine(t *testing.T) {
	dir := t.TempDir()

	// 1. Trailing newline + carriage return progress updates.
	p1 := filepath.Join(dir, "progress.log")
	content := "start\n10%|#  |\r50%|###|\r100%|#####|\ndone\n"
	if err := os.WriteFile(p1, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := ReadTail(p1, 50, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[0] != "start" || lines[1] != "100%|#####|" || lines[2] != "done" {
		t.Fatalf("unexpected progress lines: %#v", lines)
	}

	// 2. Single huge line exceeding maxBytes without newlines must NOT become empty.
	p2 := filepath.Join(dir, "huge.log")
	huge := strings.Repeat("x", 3000)
	if err := os.WriteFile(p2, []byte(huge), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err = ReadTail(p2, 50, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || len(lines[0]) != 1024 {
		t.Fatalf("expected 1 line of len 1024, got %d lines (first len %d)", len(lines), len(lines[0]))
	}

	// 3. Gzipped log file (`tsp -g`).
	p3 := filepath.Join(dir, "ts-out.gzipped")
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	_, _ = gw.Write([]byte("line-a\nline-b\n"))
	_ = gw.Close()
	if err := os.WriteFile(p3, gzBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err = ReadTail(p3, 50, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "line-a" || lines[1] != "line-b" {
		t.Fatalf("unexpected gzip lines: %#v", lines)
	}
}
