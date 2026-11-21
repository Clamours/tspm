package ui

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tspm-project/tsp-manager/internal/manager"
	"github.com/tspm-project/tsp-manager/internal/model"
	"github.com/tspm-project/tsp-manager/internal/spooler"
	"github.com/tspm-project/tsp-manager/internal/terminal"
)

type asyncResult struct {
	message string
	err     error
	kind    string
	lines   []string
	path    string
}

type uiTickMsg struct{}
type refreshTickMsg struct{}

type UI struct {
	manager *manager.Manager

	queues        []model.Queue
	selectedQueue int
	selectedTask  int
	selectedID    int
	queueOffset   int
	taskOffset    int
	focus         int // 0 queues, 1 tasks

	modal       string
	promptLabel string
	promptValue string
	promptKind  string
	promptStage int
	firstValue  string
	confirmText string
	confirmKind string

	logMode    bool
	logPath    string
	logQueue   string
	logLines   []string
	logTask    int
	logOffset  int
	logFetchAt time.Time
	infoMode   bool
	infoLines  []string
	infoOffset int

	busy       bool
	message    string
	messageAt  time.Time
	refreshing bool
	width      int
	height     int
}

func New(m *manager.Manager) *UI {
	sz := terminal.GetSize()
	return &UI{manager: m, selectedID: -1, width: sz.Width, height: sz.Height}
}

func (u *UI) Run(ctx context.Context) error {
	if !terminal.IsTerminal() {
		return fmt.Errorf("stdin is not a terminal; use `tspm queues` or `tspm status --json`")
	}
	p := tea.NewProgram(u, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

func (u *UI) Init() tea.Cmd {
	return tea.Batch(u.refreshCmd(), uiTickCmd(), u.refreshTickCmd())
}

func uiTickCmd() tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return uiTickMsg{} })
}

func (u *UI) refreshTickCmd() tea.Cmd {
	interval := time.Duration(u.manager.Config().RefreshIntervalSeconds) * time.Second
	return tea.Tick(interval, func(time.Time) tea.Msg { return refreshTickMsg{} })
}

func (u *UI) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return asyncResult{kind: "refresh", err: u.manager.Refresh(c)}
	}
}

func (u *UI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyMsg:
		return u, u.handleKey(keyOf(m))
	case tea.WindowSizeMsg:
		u.width, u.height = m.Width, m.Height
		return u, nil
	case uiTickMsg:
		if u.logMode && u.logPath != "" {
			u.refreshLog()
		}
		return u, uiTickCmd()
	case refreshTickMsg:
		cmd := u.refreshTickCmd()
		if u.refreshing {
			return u, cmd
		}
		u.refreshing = true
		return u, tea.Batch(u.refreshCmd(), cmd)
	case asyncResult:
		return u, u.handleResult(m)
	}
	return u, nil
}

func (u *UI) handleResult(result asyncResult) tea.Cmd {
	u.busy = false
	var cmd tea.Cmd
	if result.kind == "refresh" {
		u.refreshing = false
		u.syncSnapshot()
	} else if result.kind == "info" {
		if result.err != nil {
			u.setMessage("Info: " + result.err.Error())
		} else {
			u.infoMode = true
			u.infoOffset = 0
			u.infoLines = make([]string, len(result.lines))
			for i, line := range result.lines {
				u.infoLines[i] = cleanLogLine(line)
			}
		}
	} else if result.kind == "logpath" {
		if result.err != nil {
			u.setMessage("Log: " + result.err.Error())
		} else if result.path == "" {
			u.logLines = []string{"No output file is available for this task.", "The task may still be queued or was submitted with tsp -n."}
		} else {
			u.logPath = result.path
			u.refreshLog()
		}
	} else {
		if result.err != nil {
			u.setMessage(result.message + ": " + result.err.Error())
		} else if result.message != "" {
			u.setMessage(result.message)
		}
		if (result.kind == "action" || result.kind == "submit") && !u.refreshing {
			u.refreshing = true
			cmd = u.refreshCmd()
		}
	}
	u.syncSnapshot()
	return cmd
}

func (u *UI) syncSnapshot() {
	oldID := u.selectedID
	u.queues = u.manager.Snapshot()
	if len(u.queues) == 0 {
		u.selectedQueue, u.selectedTask, u.selectedID = 0, 0, -1
		return
	}
	if u.selectedQueue >= len(u.queues) {
		u.selectedQueue = len(u.queues) - 1
	}
	if u.selectedQueue < 0 {
		u.selectedQueue = 0
	}
	q := u.queues[u.selectedQueue]
	if oldID >= 0 {
		for i, t := range q.Tasks {
			if t.ID == oldID {
				u.selectedTask, u.selectedID = i, oldID
				return
			}
		}
	}
	if len(q.Tasks) == 0 {
		u.selectedTask, u.selectedID = 0, -1
	} else {
		if u.selectedTask >= len(q.Tasks) {
			u.selectedTask = len(q.Tasks) - 1
		}
		if u.selectedTask < 0 {
			u.selectedTask = 0
		}
		u.selectedID = q.Tasks[u.selectedTask].ID
	}
}

func (u *UI) currentQueue() *model.Queue {
	if len(u.queues) == 0 || u.selectedQueue < 0 || u.selectedQueue >= len(u.queues) {
		return nil
	}
	return &u.queues[u.selectedQueue]
}

func (u *UI) currentTask() *model.Task {
	q := u.currentQueue()
	if q == nil || len(q.Tasks) == 0 || u.selectedTask < 0 || u.selectedTask >= len(q.Tasks) {
		return nil
	}
	return &q.Tasks[u.selectedTask]
}

func (u *UI) setMessage(s string) { u.message, u.messageAt = s, time.Now() }

func keyOf(msg tea.KeyMsg) terminal.Key {
	switch msg.Type {
	case tea.KeyCtrlC:
		return terminal.Key{Name: "ctrl-c"}
	case tea.KeyTab:
		return terminal.Key{Name: "tab"}
	case tea.KeyEnter:
		return terminal.Key{Name: "enter"}
	case tea.KeyBackspace:
		return terminal.Key{Name: "backspace"}
	case tea.KeyEsc:
		return terminal.Key{Name: "esc"}
	case tea.KeyUp:
		return terminal.Key{Name: "up"}
	case tea.KeyDown:
		return terminal.Key{Name: "down"}
	case tea.KeyLeft:
		return terminal.Key{Name: "left"}
	case tea.KeyRight:
		return terminal.Key{Name: "right"}
	case tea.KeyHome:
		return terminal.Key{Name: "home"}
	case tea.KeyEnd:
		return terminal.Key{Name: "end"}
	case tea.KeyRunes:
		if len(msg.Runes) > 0 {
			return terminal.Key{Name: "rune", Rune: msg.Runes[0]}
		}
	}
	return terminal.Key{Name: msg.String()}
}

func (u *UI) handleKey(k terminal.Key) tea.Cmd {
	if u.modal == "prompt" {
		return u.handlePromptKey(k)
	}
	if u.modal == "confirm" {
		if k.Name == "esc" || k.Name == "rune" && (k.Rune == 'n' || k.Rune == 'N') {
			u.modal = ""
			return nil
		}
		yes := k.Name == "enter" || (k.Name == "rune" && (k.Rune == 'y' || k.Rune == 'Y'))
		if u.confirmKind == "remove" && k.Name == "rune" && (k.Rune == 'r' || k.Rune == 'R') {
			yes = true
		}
		if yes {
			return u.executeConfirmed()
		}
		return nil
	}
	if u.logMode || u.infoMode {
		if k.Name == "esc" || k.Name == "left" || k.Name == "rune" && (k.Rune == 'q' || k.Rune == 'l' || k.Rune == 'i') {
			u.logMode, u.infoMode = false, false
			u.logPath, u.logQueue, u.logLines, u.infoLines = "", "", nil, nil
			u.logOffset, u.infoOffset = 0, 0
			return nil
		}
		if u.infoMode {
			if k.Name == "up" && u.infoOffset > 0 {
				u.infoOffset--
			}
			if k.Name == "down" {
				u.infoOffset++
			}
			if k.Name == "rune" && k.Rune == 'G' {
				u.infoOffset = 0
			}
			return nil
		}
		if k.Name == "up" {
			u.logOffset++
		}
		if k.Name == "down" && u.logOffset > 0 {
			u.logOffset--
		}
		if k.Name == "rune" && k.Rune == 'G' {
			u.logOffset = 0
		}
		return nil
	}

	switch k.Name {
	case "ctrl-c":
		return tea.Quit
	case "left":
		u.focus = 0
	case "right":
		if u.focus == 0 {
			u.focus = 1
		} else {
			return u.openInfo()
		}
	case "up":
		u.moveSelection(-1)
	case "down":
		u.moveSelection(1)
	case "enter":
		if u.focus == 0 {
			u.focus = 1
		}
	case "esc":
		return tea.Quit
	case "rune":
		switch k.Rune {
		case 'q':
			return tea.Quit
		case 'a':
			if u.currentQueue() != nil {
				u.beginPrompt("submit", "Shell command to submit:")
			}
		case 'n':
			u.beginPrompt("queue-name", "New queue name:")
		case 'd':
			if q := u.currentQueue(); q != nil {
				u.confirm("remove-queue", "Remove config registration for queue '"+q.Name+"'? The tsp queue itself will not be stopped.")
			}
		case 'k':
			if t := u.currentTask(); t != nil {
				u.confirm("kill", fmt.Sprintf("Send SIGTERM to task #%d process group?", t.ID))
			}
		case 'r':
			if t := u.currentTask(); t != nil {
				u.confirm("remove", fmt.Sprintf("Remove task #%d from the queue?", t.ID))
			}
		case 'C':
			if q := u.currentQueue(); q != nil {
				u.confirm("clear", "Clear all finished tasks from queue '"+q.Name+"'?")
			}
		case 'u':
			if t := u.currentTask(); t != nil {
				return u.runUrgent(t.ID)
			}
		case 's':
			if q := u.currentQueue(); q != nil {
				u.beginPrompt("slots", "Maximum concurrent tasks (slots):")
			}
		case 'l':
			if t := u.currentTask(); t != nil {
				return u.openLogs(t.ID)
			}
		case 'i':
			return u.openInfo()
		case 'g':
			u.focus = 0
		case 't':
			u.focus = 1
		}
	}
	return nil
}

func (u *UI) moveSelection(delta int) {
	if u.focus == 0 {
		if len(u.queues) == 0 {
			return
		}
		u.selectedQueue += delta
		if u.selectedQueue < 0 {
			u.selectedQueue = 0
		}
		if u.selectedQueue >= len(u.queues) {
			u.selectedQueue = len(u.queues) - 1
		}
		u.selectedTask, u.selectedID = 0, -1
		u.taskOffset = 0
		u.syncSnapshot()
		return
	}
	q := u.currentQueue()
	if q == nil || len(q.Tasks) == 0 {
		return
	}
	u.selectedTask += delta
	if u.selectedTask < 0 {
		u.selectedTask = 0
	}
	if u.selectedTask >= len(q.Tasks) {
		u.selectedTask = len(q.Tasks) - 1
	}
	u.selectedID = q.Tasks[u.selectedTask].ID
}

func (u *UI) beginPrompt(kind, label string) {
	u.modal, u.promptKind, u.promptLabel, u.promptValue, u.promptStage = "prompt", kind, label, "", 0
}

func (u *UI) handlePromptKey(k terminal.Key) tea.Cmd {
	if k.Name == "esc" {
		u.modal = ""
		return nil
	}
	if k.Name == "backspace" {
		runes := []rune(u.promptValue)
		if len(runes) > 0 {
			u.promptValue = string(runes[:len(runes)-1])
		}
		return nil
	}
	if k.Name == "enter" {
		value := strings.TrimSpace(u.promptValue)
		switch u.promptKind {
		case "queue-name":
			if value == "" {
				u.setMessage("Queue name cannot be empty")
				return nil
			}
			u.firstValue = value
			u.promptKind, u.promptLabel, u.promptValue, u.promptStage = "queue-socket", "Socket path for "+value+":", "", 1
		case "queue-socket":
			if value == "" {
				u.setMessage("Socket path cannot be empty")
				return nil
			}
			name := u.firstValue
			u.modal = ""
			if err := u.manager.AddQueue(name, value); err != nil {
				u.setMessage(err.Error())
				return nil
			}
			u.setMessage("Registered queue " + name)
			return u.refreshNow()
		case "submit":
			if value == "" {
				u.modal = ""
				return nil
			}
			q := u.currentQueue()
			if q == nil {
				u.modal = ""
				return nil
			}
			name, command := q.Name, value
			u.modal = ""
			u.busy = true
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				id, err := u.manager.SubmitShell(ctx, name, command)
				msg := "Submitted task #" + strconv.Itoa(id) + " to " + name
				if err != nil {
					msg = "Submit failed"
				}
				return asyncResult{kind: "submit", message: msg, err: err}
			}
		case "slots":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				u.setMessage("Slots must be a positive integer")
				return nil
			}
			q := u.currentQueue()
			if q == nil {
				u.modal = ""
				return nil
			}
			name := q.Name
			u.modal = ""
			u.busy = true
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				err := u.manager.SetSlots(ctx, name, n)
				msg := fmt.Sprintf("Set %s slots to %d", name, n)
				if err != nil {
					msg = "Set slots failed"
				}
				return asyncResult{kind: "action", message: msg, err: err}
			}
		}
		return nil
	}
	if k.Name == "rune" && k.Rune >= 32 && k.Rune != 127 {
		u.promptValue += string(k.Rune)
	}
	return nil
}

func (u *UI) confirm(kind, text string) {
	u.modal, u.confirmKind, u.confirmText = "confirm", kind, text
}

func (u *UI) executeConfirmed() tea.Cmd {
	kind := u.confirmKind
	u.modal = ""
	q := u.currentQueue()
	if q == nil {
		return nil
	}
	name := q.Name
	t := u.currentTask()
	id := -1
	if t != nil {
		id = t.ID
	}
	u.busy = true
	return func() tea.Msg {
		callCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		var err error
		msg := ""
		switch kind {
		case "kill":
			err = u.manager.Kill(callCtx, name, id)
			msg = fmt.Sprintf("Sent SIGTERM to task #%d", id)
		case "remove":
			err = u.manager.Remove(callCtx, name, id)
			msg = fmt.Sprintf("Removed task #%d", id)
		case "clear":
			err = u.manager.ClearFinished(callCtx, name)
			msg = "Cleared finished tasks from " + name
		case "remove-queue":
			err = u.manager.RemoveQueue(name)
			msg = "Removed queue registration: " + name
		}
		if err != nil {
			msg = "Operation failed"
		}
		return asyncResult{kind: "action", message: msg, err: err}
	}
}

func (u *UI) runUrgent(id int) tea.Cmd {
	q := u.currentQueue()
	if q == nil {
		return nil
	}
	name := q.Name
	u.busy = true
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := u.manager.Urgent(c, name, id)
		msg := fmt.Sprintf("Moved task #%d forward", id)
		if err != nil {
			msg = "Could not reprioritize task"
		}
		return asyncResult{kind: "action", message: msg, err: err}
	}
}

func (u *UI) refreshNow() tea.Cmd {
	if u.refreshing {
		return nil
	}
	u.refreshing = true
	return u.refreshCmd()
}

func (u *UI) openInfo() tea.Cmd {
	q, t := u.currentQueue(), u.currentTask()
	if q == nil || t == nil {
		return nil
	}
	name, id, output := q.Name, t.ID, t.Output
	u.busy = true
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := u.manager.Info(c, name, id)
		if output == "" {
			if p, oerr := u.manager.OutputPath(c, name, id); oerr == nil {
				output = p
			}
		}
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if output != "" {
			lines = append(lines, "Output file: "+output)
		}
		return asyncResult{kind: "info", lines: lines, err: err}
	}
}

func (u *UI) openLogs(id int) tea.Cmd {
	q := u.currentQueue()
	if q == nil {
		return nil
	}
	name := q.Name
	output := ""
	if t := u.currentTask(); t != nil {
		output = t.Output
	}
	u.logMode, u.logTask, u.logQueue, u.logOffset = true, id, name, 0
	u.logFetchAt = time.Time{}
	u.logLines = []string{"Loading output file..."}
	u.busy = true
	return func() tea.Msg {
		if output != "" {
			return asyncResult{kind: "logpath", path: output}
		}
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		path, err := u.manager.OutputPath(c, name, id)
		return asyncResult{kind: "logpath", path: path, err: err}
	}
}

// logAnsi matches ANSI escape sequences: CSI (\x1b[...letter) and OSC
// (\x1b]...terminated by BEL or ST).
var logAnsi = regexp.MustCompile(`\x1b\[[0-9;:]*[ -/]*[A-Za-z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?`)

// cleanLogLine strips terminal-hostile sequences from a raw log line while
// keeping SGR color codes. Tabs are expanded; other control chars are dropped.
func cleanLogLine(s string) string {
	s = logAnsi.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "\x1b[") && strings.HasSuffix(m, "m") && !strings.ContainsAny(m[2:len(m)-1], " -/") {
			return m
		}
		return ""
	})
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func (u *UI) refreshLog() {
	if u.logPath == "" {
		return
	}
	var lines []string
	var err error
	if st, serr := os.Stat(u.logPath); serr == nil && st.Mode().IsRegular() {
		lines, err = spooler.ReadTail(u.logPath, 500, 256*1024)
	} else {
		// The output path is missing or not a regular file (e.g. a stale unix
		// socket left by another tsp instance): ask the daemon instead. This is
		// more expensive than a file read, so throttle it to once per second.
		if time.Since(u.logFetchAt) < time.Second {
			return
		}
		u.logFetchAt = time.Now()
		var out string
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		out, err = u.manager.TaskOutput(c, u.logQueue, u.logTask)
		cancel()
		if err == nil {
			lines = strings.Split(strings.TrimRight(strings.ReplaceAll(out, "\r\n", "\n"), "\n"), "\n")
			if len(lines) > 500 {
				lines = lines[len(lines)-500:]
			}
		}
	}
	if err != nil {
		u.logLines = []string{"Unable to read log: " + err.Error()}
		return
	}
	for i := range lines {
		lines[i] = cleanLogLine(lines[i])
	}
	u.logLines = lines
}

func (u *UI) View() string {
	w, h := u.width, u.height
	if w < 60 {
		w = 60
	}
	if h < 18 {
		h = 18
	}
	var b strings.Builder
	write := func(s string) { b.WriteString(s); b.WriteString("\n") }

	q := u.currentQueue()
	status := ""
	if u.busy {
		status = "Working..."
	}
	if u.message != "" && time.Since(u.messageAt) < 8*time.Second {
		status = u.message
	}
	if q != nil {
		if status != "" {
			status += "   |   "
		}
		status += "selected: " + q.Socket
	}
	title := " TSP MANAGER  v0.1.0"
	write(color("36;1", title) + fit("   "+status, w-len(title)))
	write(strings.Repeat("─", w))

	bodyRows := h - 5
	leftW := queuePanelWidth(u.queues, w)
	rightW := w - leftW - 3
	if rightW < 25 {
		rightW = 25
	}
	write(color("33;1", fit(" QUEUES", leftW)) + " │ " + color("33;1", fit(" TASKS", rightW)))
	u.queueOffset = scrollWindow(u.queueOffset, u.selectedQueue, bodyRows, len(u.queues))
	if q != nil {
		u.taskOffset = scrollWindow(u.taskOffset, u.selectedTask, bodyRows-1, len(q.Tasks))
	} else {
		u.taskOffset = 0
	}
	for row := 0; row < bodyRows; row++ {
		left := ""
		if qi := row + u.queueOffset; qi < len(u.queues) {
			item := u.queues[qi]
			mark := "  "
			if qi == u.selectedQueue {
				if u.focus == 0 {
					mark = color("36;1", "> ")
				} else {
					mark = color("90", "> ")
				}
			}
			state := color("31", "○")
			if item.Active {
				state = color("32", "●")
			}
			if item.Error != "" && !item.Active {
				state = color("31", "!")
			}
			name := item.Name
			count := queueCountLabel(item)
			left = mark + state + " " + name + "  " + count
		}

		right := ""
		if u.logMode {
			right = u.renderLogLine(row, bodyRows, rightW)
		} else if u.infoMode {
			right = u.renderInfoLine(row, bodyRows, rightW)
		} else if row == 0 {
			if q != nil {
				right = fmt.Sprintf("ID   %-9s %-5s %-10s %-18s %s", "STATE", "EXIT", "TIME", "OUTPUT", "COMMAND")
			} else {
				right = "No active queues discovered"
			}
		} else if q != nil && row-1+u.taskOffset < len(q.Tasks) {
			ti := row - 1 + u.taskOffset
			t := q.Tasks[ti]
			marker := "  "
			if ti == u.selectedTask {
				if u.focus == 1 {
					marker = color("36;1", "> ")
				} else {
					marker = color("90", "> ")
				}
			}
			state := stateColor(t.State)
			command := t.Command
			if command == "" {
				command = "(command unavailable)"
			}
			output := t.Output
			if output == "" {
				output = "-"
			}
			right = fmt.Sprintf("%s%-4d %-9s %-5s %-10s %-18s %s", marker, t.ID, fit(state, 9), fit(t.ExitCode, 5), fit(formatTimes(t.Times), 10), output, command)
		}
		write(fit(left, leftW) + " │ " + fit(right, rightW))
	}
	write(strings.Repeat("─", w))
	if u.modal == "prompt" {
		write(color("33;1", fit(" "+u.promptLabel+" "+u.promptValue+"▌", w)))
	} else if u.modal == "confirm" {
		hint := "y/Enter"
		if u.confirmKind == "remove" {
			hint = "r/y/Enter"
		}
		write(color("31;1", fit(" CONFIRM: "+u.confirmText+" ["+hint+" = yes, n/Esc = no]", w)))
	} else {
		write(fit(" ←→ Focus/Details  ↑↓ Navigate  a Submit  n Add queue  k Kill  r Remove  C Clear  u Urgent  s Slots  l Logs  q/Esc Quit", w))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func (u *UI) renderLogLine(row, bodyRows, width int) string {
	if row == 0 {
		return color("33;1", fmt.Sprintf(" LIVE OUTPUT  (Esc to close; ↑↓ scroll)  task #%d  %s", u.logTask, u.logPath))
	}
	lines := u.logLines
	if len(lines) == 0 {
		return "(empty output)"
	}
	visible := bodyRows - 1
	end := len(lines) - u.logOffset
	if end < 0 {
		end = 0
	}
	start := end - visible
	if start < 0 {
		start = 0
	}
	idx := start + row - 1
	if idx >= 0 && idx < end {
		return lines[idx]
	}
	return ""
}

func (u *UI) renderInfoLine(row, bodyRows, width int) string {
	if row == 0 {
		return color("33;1", " TASK INFORMATION  (Esc to close; ↑↓ scroll)")
	}
	lines := wrapLines(u.infoLines, width)
	max := len(lines) - (bodyRows - 1)
	if max < 0 {
		max = 0
	}
	if u.infoOffset > max {
		u.infoOffset = max
	}
	idx := u.infoOffset + row - 1
	if idx >= 0 && idx < len(lines) {
		return lines[idx]
	}
	return ""
}

func queueCountLabel(q model.Queue) string {
	if q.Slots < 1 {
		return fmt.Sprintf("%d/?", q.RunningCount())
	}
	return fmt.Sprintf("%d/%d", q.RunningCount(), q.Slots)
}

func queuePanelWidth(queues []model.Queue, totalWidth int) int {
	const (
		minW    = 14
		maxCap  = 32
		padding = 1
	)
	maxW := totalWidth / 3
	if maxW > maxCap {
		maxW = maxCap
	}
	if maxW < minW {
		maxW = minW
	}

	needed := minW
	for _, q := range queues {
		// Row format: mark(2) + state(1) + " "(1) + name + "  "(2) + count
		lineW := 6 + utf8.RuneCountInString(q.Name) + len(queueCountLabel(q)) + padding
		if lineW > needed {
			needed = lineW
		}
	}
	if needed > maxW {
		return maxW
	}
	return needed
}

// scrollWindow adjusts a scroll offset so that sel stays inside the visible
// window of `visible` rows out of `total` items.
func scrollWindow(offset, sel, visible, total int) int {
	if visible < 1 {
		visible = 1
	}
	if sel < offset {
		offset = sel
	}
	if sel >= offset+visible {
		offset = sel - visible + 1
	}
	if max := total - visible; offset > max {
		offset = max
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

// formatTimes renders a tsp "real/user/sys" times cell as a human-readable
// wall-clock duration, e.g. "2471.30/21.79/3.94" -> "41m11s".
func formatTimes(raw string) string {
	real, _, _ := strings.Cut(raw, "/")
	f, err := strconv.ParseFloat(real, 64)
	if err != nil {
		return raw
	}
	return time.Duration(f * float64(time.Second)).Truncate(time.Second).String()
}

func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	// Preserve ANSI color sequences while measuring visible rune width.
	var out strings.Builder
	visible := 0
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "\x1b[") {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				out.WriteString(s[i : j+1])
				i = j + 1
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if visible < width {
			if visible == width-1 && hasVisibleRune(s[i+size:]) {
				out.WriteRune('…')
				visible++
				break
			}
			out.WriteRune(r)
			visible++
		}
		i += size
	}
	if visible < width {
		out.WriteString(strings.Repeat(" ", width-visible))
	}
	result := out.String()
	if strings.Contains(result, "\x1b[") && !strings.HasSuffix(result, "\x1b[0m") {
		result += "\x1b[0m"
	}
	return result
}

// wrapLines hard-wraps each line to width visible runes so long lines (such
// as task commands) are shown in full instead of being truncated. ANSI escape
// sequences are passed through without counting toward the width.
func wrapLines(lines []string, width int) []string {
	if width < 1 {
		return lines
	}
	var out []string
	for _, s := range lines {
		for {
			var b strings.Builder
			visible, i := 0, 0
			for i < len(s) && visible < width {
				if strings.HasPrefix(s[i:], "\x1b[") {
					j := i + 2
					for j < len(s) && s[j] != 'm' {
						j++
					}
					if j < len(s) {
						b.WriteString(s[i : j+1])
						i = j + 1
						continue
					}
				}
				r, size := utf8.DecodeRuneInString(s[i:])
				b.WriteRune(r)
				visible++
				i += size
			}
			out = append(out, b.String())
			if s = s[i:]; s == "" {
				break
			}
		}
	}
	return out
}

func hasVisibleRune(s string) bool {
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "\x1b[") {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		return true
	}
	return false
}

func color(code, s string) string { return "\x1b[" + code + "m" + s + "\x1b[0m" }

func stateColor(s string) string {
	switch strings.ToLower(s) {
	case "running":
		return color("32;1", s)
	case "queued", "waiting":
		return color("33", s)
	case "finished", "done":
		return color("36", s)
	case "failed":
		return color("31;1", s)
	default:
		return s
	}
}
