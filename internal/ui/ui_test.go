package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
	"github.com/Clamours/tspm/internal/config"
	"github.com/Clamours/tspm/internal/manager"
	"github.com/Clamours/tspm/internal/model"
	"github.com/Clamours/tspm/internal/terminal"
)

func TestFormatTimes(t *testing.T) {
	cases := map[string]string{
		"2471.30/21.79/3.94": "41m11s",
		"1479.85/21.60/3.52": "24m39s",
		"58.20/44.41/24.93":  "58s",
		"58.20":              "58s",
		"3661.5/1/1":         "1h1m1s",
		"90061":              "25h1m1s",
		"?":                  "?",
		"":                   "",
	}
	for in, want := range cases {
		if got := formatTimes(in); got != want {
			t.Errorf("formatTimes(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQueuePanelWidth(t *testing.T) {
	cases := []struct {
		name       string
		queues     []model.Queue
		totalWidth int
		want       int
	}{
		{
			name:       "empty uses minimum width",
			queues:     nil,
			totalWidth: 120,
			want:       14,
		},
		{
			name:       "short queue name adapts tightly",
			queues:     []model.Queue{{Name: "default", Slots: 1}},
			totalWidth: 120,
			want:       17, // 6 + 7 + 3 + 1
		},
		{
			name: "fits longest queue name",
			queues: []model.Queue{
				{Name: "default", Slots: 1},
				{Name: "score-pptarena", Slots: 1},
				{Name: "scoring", Slots: 1},
			},
			totalWidth: 140,
			want:       24, // 6 + 14 + 3 + 1
		},
		{
			name:       "cjk queue name uses display width",
			queues:     []model.Queue{{Name: "训练队列", Slots: 1}},
			totalWidth: 120,
			want:       18, // 6 + 8 + 3 + 1
		},
		{
			name:       "clamps to max cap on wide terminal",
			queues:     []model.Queue{{Name: "very-long-queue-name-exceeding-limit", Slots: 4}},
			totalWidth: 150,
			want:       32,
		},
		{
			name:       "clamps to one third on narrow terminal",
			queues:     []model.Queue{{Name: "score-pptarena", Slots: 1}},
			totalWidth: 60,
			want:       20,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := queuePanelWidth(tc.queues, tc.totalWidth); got != tc.want {
				t.Errorf("queuePanelWidth() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestKeyOfSpacePasteAndBackspace(t *testing.T) {
	sp := keyOf(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if sp.Name != "rune" || sp.Rune != ' ' || string(sp.Runes) != " " {
		t.Fatalf("space key mismatch: %#v", sp)
	}

	paste := keyOf(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("python train.py --epochs 3"), Paste: true})
	if paste.Name != "rune" || string(paste.Runes) != "python train.py --epochs 3" {
		t.Fatalf("paste key mismatch: %#v", paste)
	}

	bs := keyOf(tea.KeyMsg{Type: tea.KeyCtrlH})
	if bs.Name != "backspace" {
		t.Fatalf("ctrl-h should map to backspace, got %#v", bs)
	}
}

func TestPromptSupportsSpacePasteAndEditing(t *testing.T) {
	m := manager.New(config.Default(), filepath.Join(t.TempDir(), "config.json"))
	u := New(m, "0.2.0")
	u.beginPrompt("submit", "Shell command:", "gpu0", "/tmp/tsp-gpu0.sock")

	u.handleKey(keyOf(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("python")}))
	u.handleKey(keyOf(tea.KeyMsg{Type: tea.KeySpace}))
	u.handleKey(keyOf(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("train.py --epochs 3"), Paste: true}))
	if u.promptValue != "python train.py --epochs 3" {
		t.Fatalf("unexpected promptValue: %q", u.promptValue)
	}

	u.handleKey(keyOf(tea.KeyMsg{Type: tea.KeyCtrlW}))
	if u.promptValue != "python train.py --epochs " {
		t.Fatalf("after ctrl-w expected trailing space, got %q", u.promptValue)
	}

	u.handleKey(keyOf(tea.KeyMsg{Type: tea.KeyCtrlU}))
	if u.promptValue != "" {
		t.Fatalf("after ctrl-u expected empty, got %q", u.promptValue)
	}
}

func TestCleanLogLinePreservesSGRAndStripsHostileANSI(t *testing.T) {
	raw := "\x1b[32;1mOK\x1b[0m \x1b[?25l\x1b[2J\x1b]0;title\x07\thello\x07world"
	got := cleanLogLine(raw)
	want := "\x1b[32;1mOK\x1b[0m     helloworld"
	if got != want {
		t.Fatalf("cleanLogLine(%q) = %q, want %q", raw, got, want)
	}
}

func TestFitAndWrapCJKDisplayWidth(t *testing.T) {
	fitted := fit("训练任务abc", 7)
	if w := ansiDisplayWidth(fitted); w != 7 {
		t.Fatalf("fit width = %d, want 7 (%q)", w, fitted)
	}
	if !strings.Contains(fitted, "…") {
		t.Fatalf("expected ellipsis in %q", fitted)
	}

	colored := fit(color("32;1", "训练任务abc"), 7)
	if w := ansiDisplayWidth(colored); w != 7 {
		t.Fatalf("colored fit width = %d, want 7 (%q)", w, colored)
	}
	if !strings.HasSuffix(colored, "\x1b[0m") {
		t.Fatalf("colored fit must reset ANSI: %q", colored)
	}

	wrapped := wrapLines([]string{"中文测试ABCDEF"}, 6)
	for _, line := range wrapped {
		if w := runewidth.StringWidth(line); w > 6 {
			t.Fatalf("wrapped line %q exceeds width 6 (got %d)", line, w)
		}
	}
}

func TestCtrlCAlwaysQuitsInModalsAndOverlays(t *testing.T) {
	m := manager.New(config.Default(), filepath.Join(t.TempDir(), "config.json"))
	u := New(m, "0.1.0")

	u.modal = "prompt"
	if cmd := u.handleKey(terminal.Key{Name: "ctrl-c"}); cmd == nil {
		t.Fatal("ctrl-c in prompt should return quit cmd")
	}
	u.modal = ""
	u.logMode = true
	if cmd := u.handleKey(terminal.Key{Name: "ctrl-c"}); cmd == nil {
		t.Fatal("ctrl-c in logMode should return quit cmd")
	}
	u.logMode = false
	u.helpMode = true
	if cmd := u.handleKey(terminal.Key{Name: "ctrl-c"}); cmd == nil {
		t.Fatal("ctrl-c in helpMode should return quit cmd")
	}
}

func TestConfirmLocksTargetAcrossQueueChanges(t *testing.T) {
	m := manager.New(config.Default(), filepath.Join(t.TempDir(), "config.json"))
	u := New(m, "0.1.0")
	u.queues = []model.Queue{
		{
			Name:   "gpu0",
			Socket: "/tmp/tsp-gpu0.sock",
			Active: true,
			Tasks:  []model.Task{{ID: 7, State: "running", Command: "train"}},
		},
		{
			Name:   "gpu1",
			Socket: "/tmp/tsp-gpu1.sock",
			Active: true,
			Tasks:  []model.Task{{ID: 99, State: "running", Command: "eval"}},
		},
	}
	u.selectedQueue = 0
	u.selectedSocket = "/tmp/tsp-gpu0.sock"
	u.selectedTask = 0
	u.selectedID = 7

	u.handleKey(terminal.Key{Name: "rune", Rune: 'k'})
	if u.modal != "confirm" || u.targetQueueSocket != "/tmp/tsp-gpu0.sock" || u.targetTaskID != 7 {
		t.Fatalf("confirm did not lock target: modal=%q socket=%q id=%d", u.modal, u.targetQueueSocket, u.targetTaskID)
	}

	// Simulate selection shifting to another queue/task while confirm dialog is open.
	u.selectedQueue = 1
	u.selectedSocket = "/tmp/tsp-gpu1.sock"
	u.selectedTask = 0
	u.selectedID = 99
	if u.targetQueueSocket != "/tmp/tsp-gpu0.sock" || u.targetTaskID != 7 {
		t.Fatalf("locked target mutated after selection change")
	}
}

func TestLogScrollClampAndEmptyLogRendering(t *testing.T) {
	m := manager.New(config.Default(), filepath.Join(t.TempDir(), "config.json"))
	u := New(m, "0.1.0")
	u.width = 100
	u.height = 12 // bodyRows = 7, visible = 6
	u.logMode = true
	u.logLines = []string{"line1", "line2", "line3", "line4", "line5", "line6", "line7", "line8"}

	// Press up 20 times: offset must clamp to maxOff = 2, never blanking screen.
	for i := 0; i < 20; i++ {
		u.handleKey(terminal.Key{Name: "up"})
	}
	if u.logOffset != 2 {
		t.Fatalf("logOffset = %d, want clamped 2", u.logOffset)
	}

	// Empty output should appear only once in View, not repeated on every row.
	u.logLines = nil
	u.logOffset = 0
	view := u.View()
	if c := strings.Count(view, "(empty output)"); c != 1 {
		t.Fatalf("expected '(empty output)' once, got %d times", c)
	}
}

func TestStaleAsyncLogResultIgnoredAndBusyCounter(t *testing.T) {
	m := manager.New(config.Default(), filepath.Join(t.TempDir(), "config.json"))
	u := New(m, "0.1.0")
	u.activeOps = 1
	// Periodic refresh must NOT clear activeOps.
	u.handleResult(asyncResult{kind: "refresh"})
	if u.activeOps != 1 {
		t.Fatalf("refresh cleared activeOps: got %d", u.activeOps)
	}

	// Stale logpath from an earlier reqID must be ignored.
	u.logMode = true
	u.logReqID = 5
	u.logLines = []string{"Loading output file..."}
	u.handleResult(asyncResult{kind: "logpath", reqID: 4, path: "/tmp/old.log"})
	if u.logPath != "" {
		t.Fatalf("stale logpath overwrote logPath: %q", u.logPath)
	}

	// Real logpath error should exit logMode cleanly instead of hanging on Loading.
	tmpFile := filepath.Join(t.TempDir(), "out.log")
	_ = os.WriteFile(tmpFile, []byte("hello\n"), 0o600)
	u.activeOps = 1
	u.handleResult(asyncResult{kind: "logpath", reqID: 5, err: os.ErrPermission})
	if u.logMode {
		t.Fatal("logpath error should close logMode")
	}
}

func TestTabEnterHelpAndVersionRendering(t *testing.T) {
	m := manager.New(config.Default(), filepath.Join(t.TempDir(), "config.json"))
	u := New(m, "9.9.9")
	u.width = 120
	u.height = 24
	u.queues = []model.Queue{{Name: "gpu0", Socket: "/tmp/tsp-gpu0.sock", Active: false, Error: "socket path not found"}}

	if u.focus != 0 {
		t.Fatalf("initial focus = %d, want 0", u.focus)
	}
	u.handleKey(terminal.Key{Name: "tab"})
	if u.focus != 1 {
		t.Fatalf("after Tab focus = %d, want 1", u.focus)
	}
	u.handleKey(terminal.Key{Name: "tab"})
	if u.focus != 0 {
		t.Fatalf("after second Tab focus = %d, want 0", u.focus)
	}

	view := u.View()
	if !strings.Contains(view, "v9.9.9") {
		t.Fatalf("expected version v9.9.9 in view")
	}
	if !strings.Contains(view, "Queue offline: socket path not found") {
		t.Fatalf("expected offline queue error to be displayed in view")
	}

	u.handleKey(terminal.Key{Name: "rune", Rune: '?'})
	if !u.helpMode {
		t.Fatal("? should enable helpMode")
	}
	if !strings.Contains(u.View(), "KEYBOARD SHORTCUTS") {
		t.Fatal("expected help overlay header")
	}
	u.handleKey(terminal.Key{Name: "esc"})
	if u.helpMode {
		t.Fatal("esc should close helpMode")
	}
}
