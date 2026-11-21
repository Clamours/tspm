package spooler

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseModernTaskList(t *testing.T) {
	header := "ID   State      Output               E-Level  Times(r/u/s)   Command [run=1/1]"
	rows := []string{
		fmt.Sprintf("%-4s %-10s %-20s %-8s %-14s %s", "1", "running", "/tmp/ts-out.abcd", "", "", "python train.py --epochs 3"),
		fmt.Sprintf("%-4s %-10s %-20s %-8s %-14s %s", "2", "queued", "(file)", "", "", "python eval.py"),
		fmt.Sprintf("%-4s %-10s %-20s %-8s %-14s %s", "0", "finished", "/tmp/ts-out.old", "0", "10.04/0.00/0.00", "python prepare.py"),
	}
	input := header + "\n" + strings.Join(rows, "\n")
	tasks, err := ParseTaskList(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Fatalf("got %d tasks", len(tasks))
	}
	if tasks[0].ID != 1 || tasks[0].State != "running" {
		t.Fatalf("unexpected task 0: %#v", tasks[0])
	}
	if tasks[0].Command != "python train.py --epochs 3" {
		t.Fatalf("unexpected running command: %q", tasks[0].Command)
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
