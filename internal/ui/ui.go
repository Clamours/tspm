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
	"github.com/mattn/go-runewidth"
	"github.com/tspm-project/tsp-manager/internal/manager"
	"github.com/tspm-project/tsp-manager/internal/model"
	"github.com/tspm-project/tsp-manager/internal/spooler"
	"github.com/tspm-project/tsp-manager/internal/terminal"
)

type asyncResult struct {
	message         string
	err             error
	kind            string
	lines           []string
	path            string
	reqID           uint64
	skipped         bool
	fetchedFallback bool
}

type uiTickMsg struct{}
type refreshTickMsg struct{}

type UI struct {
	manager *manager.Manager
	version string

	queues         []model.Queue
	selectedQueue  int
	selectedSocket string
	selectedTask   int
	selectedID     int
	queueOffset    int
	taskOffset     int
	focus          int // 0 queues, 1 tasks

	modal             string
	promptLabel       string
	promptValue       string
	promptKind        string
	promptStage       int
	firstValue        string
	confirmText       string
	confirmKind       string
	targetQueueName   string
	targetQueueSocket string
	targetTaskID      int

	logMode        bool
	logPath        string
	logQueueSocket string
	logLines       []string
	logTask        int
	logOffset      int
	logFetchAt     time.Time
	logFetching    bool
	logReqID       uint64

	infoMode   bool
	infoLines  []string
	infoOffset int
	infoReqID  uint64

	helpMode   bool
	helpOffset int

	activeOps  int
	message    string
	messageAt  time.Time
	refreshing bool
	width      int
	height     int
}

func New(m *manager.Manager, version string) *UI {
	sz := terminal.GetSize()
	if strings.TrimSpace(version) == "" {
		version = "0.1.0"
	}
	return &UI{
		manager:    m,
		version:    version,
		selectedID: -1,
		width:      sz.Width,
		height:     sz.Height,
	}
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
	u.refreshing = true
	return tea.Batch(u.refreshCmd(), uiTickCmd(), u.refreshTickCmd())
}

func uiTickCmd() tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return uiTickMsg{} })
}

func (u *UI) refreshTickCmd() tea.Cmd {
	interval := time.Duration(u.manager.Config().RefreshIntervalSeconds) * time.Second
	if interval < time.Second {
		interval = 2 * time.Second
	}
	return tea.Tick(interval, func(time.Time) tea.Msg { return refreshTickMsg{} })
}

func (u *UI) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return asyncResult{kind: "refresh", err: u.manager.Refresh(c)}
	}
}

func (u *UI) fetchLogCmd(reqID uint64, logPath, queueSocket string, taskID int, lastFallbackAt time.Time) tea.Cmd {
	return func() tea.Msg {
		if logPath == "" {
			return asyncResult{kind: "logdata", reqID: reqID, skipped: true}
		}
		var lines []string
		var err error
		fetchedFallback := false
		if st, serr := os.Stat(logPath); serr == nil && st.Mode().IsRegular() {
			lines, err = spooler.ReadTail(logPath, 500, 256*1024)
		} else {
			if !lastFallbackAt.IsZero() && time.Since(lastFallbackAt) < time.Second {
				return asyncResult{kind: "logdata", reqID: reqID, skipped: true}
			}
			fetchedFallback = true
			c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			out, oerr := u.manager.TaskOutput(c, queueSocket, taskID)
			cancel()
			err = oerr
			if err == nil {
				lines = spooler.NormalizeOutputLines(out, false, 500)
			}
		}
		if err == nil {
			for i := range lines {
				lines[i] = cleanLogLine(lines[i])
			}
		}
		return asyncResult{
			kind:            "logdata",
			reqID:           reqID,
			lines:           lines,
			err:             err,
			fetchedFallback: fetchedFallback,
		}
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
		if u.logMode && u.logPath != "" && !u.logFetching {
			u.logFetching = true
			return u, tea.Batch(uiTickCmd(), u.fetchLogCmd(u.logReqID, u.logPath, u.logQueueSocket, u.logTask, u.logFetchAt))
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
	var cmd tea.Cmd
	switch result.kind {
	case "refresh":
		u.refreshing = false
		if result.err != nil {
			u.setMessage("Refresh: " + result.err.Error())
		}
		u.syncSnapshot()
		return nil
	case "logdata":
		u.logFetching = false
		if result.fetchedFallback {
			u.logFetchAt = time.Now()
		}
		if !u.logMode || result.reqID != u.logReqID || result.skipped {
			return nil
		}
		if result.err != nil {
			u.logLines = []string{"Unable to read log: " + result.err.Error()}
			return nil
		}
		u.logLines = result.lines
		return nil
	case "info":
		if u.activeOps > 0 {
			u.activeOps--
		}
		if result.reqID != u.infoReqID {
			return nil
		}
		if result.err != nil {
			u.setMessage("Info: " + result.err.Error())
		} else {
			u.logMode = false
			u.helpMode = false
			u.infoMode = true
			u.infoOffset = 0
			u.infoLines = make([]string, len(result.lines))
			for i, line := range result.lines {
				u.infoLines[i] = cleanLogLine(line)
			}
		}
	case "logpath":
		if u.activeOps > 0 {
			u.activeOps--
		}
		if !u.logMode || result.reqID != u.logReqID {
			return nil
		}
		if result.err != nil {
			u.logMode = false
			u.logPath, u.logQueueSocket, u.logLines = "", "", nil
			u.setMessage("Log: " + result.err.Error())
		} else if result.path == "" {
			u.logLines = []string{"No output file is available for this task.", "The task may still be queued or was submitted with tsp -n."}
		} else {
			u.logPath = result.path
			u.logFetching = true
			cmd = u.fetchLogCmd(u.logReqID, u.logPath, u.logQueueSocket, u.logTask, u.logFetchAt)
		}
	default:
		if u.activeOps > 0 {
			u.activeOps--
		}
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
	oldSocket := u.selectedSocket
	oldID := u.selectedID
	u.queues = u.manager.Snapshot()
	if len(u.queues) == 0 {
		u.selectedQueue, u.selectedSocket, u.selectedTask, u.selectedID = 0, "", 0, -1
		return
	}
	if oldSocket != "" {
		matched := false
		for i, q := range u.queues {
			if q.Socket == oldSocket {
				u.selectedQueue = i
				matched = true
				break
			}
		}
		if !matched {
			if u.selectedQueue >= len(u.queues) {
				u.selectedQueue = len(u.queues) - 1
			}
			if u.selectedQueue < 0 {
				u.selectedQueue = 0
			}
			// Queue changed, so don't reuse old task ID from a different queue.
			oldID = -1
			u.selectedTask = 0
			u.taskOffset = 0
		}
	} else {
		if u.selectedQueue >= len(u.queues) {
			u.selectedQueue = len(u.queues) - 1
		}
		if u.selectedQueue < 0 {
			u.selectedQueue = 0
		}
	}

	q := u.queues[u.selectedQueue]
	u.selectedSocket = q.Socket
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
	case tea.KeyCtrlU:
		return terminal.Key{Name: "ctrl-u"}
	case tea.KeyCtrlW:
		return terminal.Key{Name: "ctrl-w"}
	case tea.KeyTab:
		return terminal.Key{Name: "tab"}
	case tea.KeyEnter:
		return terminal.Key{Name: "enter"}
	case tea.KeyBackspace, tea.KeyCtrlH:
		return terminal.Key{Name: "backspace"}
	case tea.KeyDelete:
		return terminal.Key{Name: "delete"}
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
	case tea.KeyPgUp:
		return terminal.Key{Name: "pgup"}
	case tea.KeyPgDown:
		return terminal.Key{Name: "pgdown"}
	case tea.KeySpace:
		return terminal.Key{Name: "rune", Rune: ' ', Runes: []rune{' '}}
	case tea.KeyRunes:
		if len(msg.Runes) > 0 {
			runes := append([]rune(nil), msg.Runes...)
			return terminal.Key{Name: "rune", Rune: runes[0], Runes: runes}
		}
	}
	return terminal.Key{Name: msg.String()}
}

func (u *UI) layoutDims() (w, h, bodyRows, leftW, rightW int) {
	w, h = u.width, u.height
	if w <= 0 {
		w = 100
	}
	if h <= 0 {
		h = 30
	}
	if w < 30 {
		w = 30
	}
	if h < 8 {
		h = 8
	}
	bodyRows = h - 5
	if bodyRows < 1 {
		bodyRows = 1
	}
	leftW = queuePanelWidth(u.queues, w)
	rightW = w - leftW - 3
	if rightW < 10 {
		rightW = 10
		if w > rightW+3 {
			leftW = w - rightW - 3
		}
	}
	return w, h, bodyRows, leftW, rightW
}

func (u *UI) pageStep() int {
	_, _, bodyRows, _, _ := u.layoutDims()
	step := bodyRows - 2
	if step < 1 {
		step = 1
	}
	return step
}

func (u *UI) maxOverlayOffset(lines []string) int {
	_, _, bodyRows, _, rightW := u.layoutDims()
	wrapped := wrapLines(lines, rightW)
	visible := bodyRows - 1
	if visible < 1 {
		visible = 1
	}
	if len(wrapped) <= visible {
		return 0
	}
	return len(wrapped) - visible
}

func (u *UI) handleKey(k terminal.Key) tea.Cmd {
	if k.Name == "ctrl-c" {
		return tea.Quit
	}
	if u.modal == "prompt" {
		return u.handlePromptKey(k)
	}
	if u.modal == "confirm" {
		if k.Name == "esc" || (k.Name == "rune" && (k.Rune == 'n' || k.Rune == 'N')) {
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
	if u.helpMode {
		if k.Name == "esc" || k.Name == "left" || (k.Name == "rune" && (k.Rune == 'q' || k.Rune == '?')) {
			u.helpMode = false
			u.helpOffset = 0
			return nil
		}
		maxOff := u.maxOverlayOffset(helpLines())
		u.helpOffset = scrollOverlayTopDown(u.helpOffset, maxOff, u.pageStep(), k)
		return nil
	}
	if u.logMode || u.infoMode {
		if k.Name == "esc" || k.Name == "left" || (k.Name == "rune" && (k.Rune == 'q' || k.Rune == 'l' || k.Rune == 'i')) {
			u.logMode, u.infoMode = false, false
			u.logReqID++
			u.infoReqID++
			u.logPath, u.logQueueSocket, u.logLines, u.infoLines = "", "", nil, nil
			u.logOffset, u.infoOffset = 0, 0
			u.logFetching = false
			return nil
		}
		if u.infoMode {
			maxOff := u.maxOverlayOffset(u.infoLines)
			u.infoOffset = scrollOverlayTopDown(u.infoOffset, maxOff, u.pageStep(), k)
			return nil
		}
		maxOff := u.maxOverlayOffset(u.logLines)
		u.logOffset = scrollOverlayBottomUp(u.logOffset, maxOff, u.pageStep(), k)
		return nil
	}

	switch k.Name {
	case "tab":
		u.focus = 1 - u.focus
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
		} else {
			return u.openInfo()
		}
	case "esc":
		return tea.Quit
	case "rune":
		switch k.Rune {
		case 'q':
			return tea.Quit
		case '?':
			u.helpMode = true
			u.helpOffset = 0
		case 'R':
			u.setMessage("Refreshing queues...")
			return u.refreshNow()
		case 'a':
			if q := u.currentQueue(); q != nil {
				u.beginPrompt("submit", "Shell command to submit:", q.Name, q.Socket)
			}
		case 'n':
			u.beginPrompt("queue-name", "New queue name:", "", "")
		case 'd':
			if q := u.currentQueue(); q != nil {
				u.confirm("remove-queue", "Remove config registration for queue '"+q.Name+"'? The tsp queue itself will not be stopped.", q.Name, q.Socket, -1)
			}
		case 'k':
			if q, t := u.currentQueue(), u.currentTask(); q != nil && t != nil {
				u.confirm("kill", fmt.Sprintf("Send SIGTERM to task #%d in %s?", t.ID, q.Name), q.Name, q.Socket, t.ID)
			}
		case 'r':
			if q, t := u.currentQueue(), u.currentTask(); q != nil && t != nil {
				u.confirm("remove", fmt.Sprintf("Remove task #%d from %s?", t.ID, q.Name), q.Name, q.Socket, t.ID)
			}
		case 'C':
			if q := u.currentQueue(); q != nil {
				u.confirm("clear", "Clear all finished tasks from queue '"+q.Name+"'?", q.Name, q.Socket, -1)
			}
		case 'u':
			if q, t := u.currentQueue(), u.currentTask(); q != nil && t != nil {
				return u.runUrgent(q.Name, q.Socket, t.ID)
			}
		case 's':
			if q := u.currentQueue(); q != nil {
				u.beginPrompt("slots", "Maximum concurrent tasks (slots):", q.Name, q.Socket)
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

func scrollOverlayTopDown(offset, maxOff, pageStep int, k terminal.Key) int {
	switch k.Name {
	case "up":
		offset--
	case "down":
		offset++
	case "pgup":
		offset -= pageStep
	case "pgdown":
		offset += pageStep
	case "home":
		offset = 0
	case "end":
		offset = maxOff
	case "rune":
		switch k.Rune {
		case 'k':
			offset--
		case 'j':
			offset++
		case 'g':
			offset = 0
		case 'G':
			offset = maxOff
		}
	}
	if offset < 0 {
		return 0
	}
	if offset > maxOff {
		return maxOff
	}
	return offset
}

func scrollOverlayBottomUp(offset, maxOff, pageStep int, k terminal.Key) int {
	switch k.Name {
	case "up":
		offset++
	case "down":
		offset--
	case "pgup":
		offset += pageStep
	case "pgdown":
		offset -= pageStep
	case "home":
		offset = maxOff
	case "end":
		offset = 0
	case "rune":
		switch k.Rune {
		case 'k':
			offset++
		case 'j':
			offset--
		case 'g':
			offset = maxOff
		case 'G':
			offset = 0
		}
	}
	if offset < 0 {
		return 0
	}
	if offset > maxOff {
		return maxOff
	}
	return offset
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
		u.selectedSocket = u.queues[u.selectedQueue].Socket
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

func (u *UI) beginPrompt(kind, label, queueName, queueSocket string) {
	u.modal = "prompt"
	u.promptKind = kind
	u.promptLabel = label
	u.promptValue = ""
	u.promptStage = 0
	u.targetQueueName = queueName
	u.targetQueueSocket = queueSocket
	u.targetTaskID = -1
}

func (u *UI) handlePromptKey(k terminal.Key) tea.Cmd {
	if k.Name == "esc" {
		u.modal = ""
		return nil
	}
	if k.Name == "ctrl-u" {
		u.promptValue = ""
		return nil
	}
	if k.Name == "ctrl-w" {
		runes := []rune(u.promptValue)
		end := len(runes)
		for end > 0 && runes[end-1] == ' ' {
			end--
		}
		for end > 0 && runes[end-1] != ' ' {
			end--
		}
		u.promptValue = string(runes[:end])
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
			name, socket := u.targetQueueName, u.targetQueueSocket
			if socket == "" {
				u.modal = ""
				return nil
			}
			command := value
			u.modal = ""
			u.activeOps++
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				id, err := u.manager.SubmitShell(ctx, socket, command)
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
			name, socket := u.targetQueueName, u.targetQueueSocket
			if socket == "" {
				u.modal = ""
				return nil
			}
			u.modal = ""
			u.activeOps++
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				err := u.manager.SetSlots(ctx, socket, n)
				msg := fmt.Sprintf("Set %s slots to %d", name, n)
				if err != nil {
					msg = "Set slots failed"
				}
				return asyncResult{kind: "action", message: msg, err: err}
			}
		}
		return nil
	}
	if k.Name == "rune" {
		runes := k.Runes
		if len(runes) == 0 && k.Rune != 0 {
			runes = []rune{k.Rune}
		}
		var b strings.Builder
		for _, r := range runes {
			if r >= 32 && r != 127 {
				b.WriteRune(r)
			}
		}
		if b.Len() > 0 {
			u.promptValue += b.String()
		}
	}
	return nil
}

func (u *UI) confirm(kind, text, queueName, queueSocket string, taskID int) {
	u.modal = "confirm"
	u.confirmKind = kind
	u.confirmText = text
	u.targetQueueName = queueName
	u.targetQueueSocket = queueSocket
	u.targetTaskID = taskID
}

func (u *UI) executeConfirmed() tea.Cmd {
	kind := u.confirmKind
	name := u.targetQueueName
	socket := u.targetQueueSocket
	id := u.targetTaskID
	u.modal = ""
	if socket == "" && name == "" {
		return nil
	}
	u.activeOps++
	return func() tea.Msg {
		callCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		var err error
		msg := ""
		switch kind {
		case "kill":
			err = u.manager.Kill(callCtx, socket, id)
			msg = fmt.Sprintf("Sent SIGTERM to task #%d", id)
		case "remove":
			err = u.manager.Remove(callCtx, socket, id)
			msg = fmt.Sprintf("Removed task #%d", id)
		case "clear":
			err = u.manager.ClearFinished(callCtx, socket)
			msg = "Cleared finished tasks from " + name
		case "remove-queue":
			err = u.manager.RemoveQueue(socket)
			msg = "Removed queue registration: " + name
		}
		if err != nil {
			msg = "Operation failed"
		}
		return asyncResult{kind: "action", message: msg, err: err}
	}
}

func (u *UI) runUrgent(queueName, queueSocket string, id int) tea.Cmd {
	u.activeOps++
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := u.manager.Urgent(c, queueSocket, id)
		msg := fmt.Sprintf("Moved task #%d forward in %s", id, queueName)
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
	socket, id, output := q.Socket, t.ID, t.Output
	u.infoReqID++
	reqID := u.infoReqID
	u.activeOps++
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := u.manager.Info(c, socket, id)
		if err == nil && output == "" {
			if p, oerr := u.manager.OutputPath(c, socket, id); oerr == nil {
				output = p
			}
		}
		lines := spooler.NormalizeOutputLines(out, false, 0)
		if output != "" {
			lines = append(lines, "Output file: "+output)
		}
		return asyncResult{kind: "info", reqID: reqID, lines: lines, err: err}
	}
}

func (u *UI) openLogs(id int) tea.Cmd {
	q := u.currentQueue()
	if q == nil {
		return nil
	}
	socket := q.Socket
	output := ""
	if t := u.currentTask(); t != nil {
		output = t.Output
	}
	u.infoMode = false
	u.helpMode = false
	u.logMode = true
	u.logTask = id
	u.logQueueSocket = socket
	u.logPath = ""
	u.logOffset = 0
	u.logFetchAt = time.Time{}
	u.logFetching = false
	u.logReqID++
	reqID := u.logReqID
	u.logLines = []string{"Loading output file..."}
	u.activeOps++
	return func() tea.Msg {
		if output != "" {
			return asyncResult{kind: "logpath", reqID: reqID, path: output}
		}
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		path, err := u.manager.OutputPath(c, socket, id)
		return asyncResult{kind: "logpath", reqID: reqID, path: path, err: err}
	}
}

// ansiSeq matches ANSI escape sequences:
// - CSI (\x1b[ params intermediates final, including private prefixes ? < = >)
// - OSC (\x1b]... terminated by BEL or ST)
// - 2-byte ESC sequences (\x1b(B, \x1bM, etc.)
var ansiSeq = regexp.MustCompile(`\x1b\[[0-9:;<=>?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?|\x1b[()][A-Za-z0-9]|\x1b[@-Z\\-_]`)

func isSafeSGR(seq string) bool {
	if !strings.HasPrefix(seq, "\x1b[") || !strings.HasSuffix(seq, "m") {
		return false
	}
	params := seq[2 : len(seq)-1]
	for i := 0; i < len(params); i++ {
		c := params[i]
		if (c < '0' || c > '9') && c != ';' && c != ':' {
			return false
		}
	}
	return true
}

func stripControls(s string, tabReplacement string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\t' {
			b.WriteString(tabReplacement)
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// cleanLogLine strips terminal-hostile sequences and control chars from a raw
// log line while preserving SGR color codes verbatim (including their ESC byte).
func cleanLogLine(s string) string {
	matches := ansiSeq.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return stripControls(s, "    ")
	}
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for _, m := range matches {
		if m[0] > last {
			b.WriteString(stripControls(s[last:m[0]], "    "))
		}
		seq := s[m[0]:m[1]]
		if isSafeSGR(seq) {
			b.WriteString(seq)
		}
		last = m[1]
	}
	if last < len(s) {
		b.WriteString(stripControls(s[last:], "    "))
	}
	return b.String()
}

// cleanTableCell strips all ANSI sequences and control characters for single-line table cells.
func cleanTableCell(s string) string {
	s = ansiSeq.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\t' || r == '\n' || r == '\r' {
			b.WriteByte(' ')
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func helpLines() []string {
	return []string{
		"Navigation",
		"  ↑ / ↓         Move selection in active pane",
		"  Tab           Switch between QUEUES and TASKS panes",
		"  ← / →         Focus QUEUES / focus TASKS or open details",
		"  g / t         Jump focus to QUEUES / TASKS",
		"  Enter         Open TASKS pane or view selected task details",
		"",
		"Task Actions",
		"  a             Submit shell command to current queue",
		"  i             View task information (tsp -i)",
		"  l             View live task output (tail / refresh)",
		"  k             Send SIGTERM to task process group (tsp -k)",
		"  r             Remove task from queue (tsp -r)",
		"  u             Move queued task forward (tsp -u)",
		"  C             Clear finished tasks in current queue (tsp -C)",
		"",
		"Queue Actions",
		"  n             Register a custom queue socket",
		"  d             Remove queue config registration (does not stop tsp)",
		"  s             Set maximum concurrent slots (tsp -S)",
		"  R             Refresh all queues immediately",
		"",
		"Overlays (Logs / Details / Help)",
		"  ↑ / ↓ / j / k Scroll line by line",
		"  PgUp / PgDn   Scroll by page",
		"  g / Home      Jump to top",
		"  G / End       Jump to bottom",
		"  Esc / ← / q   Close overlay",
		"",
		"Prompt Editing",
		"  Backspace     Delete previous character",
		"  Ctrl-W        Delete previous word",
		"  Ctrl-U        Clear input",
		"  Esc           Cancel prompt",
		"",
		"General",
		"  ?             Show this help",
		"  q / Esc       Quit",
		"  Ctrl-C        Quit immediately from any screen",
	}
}

func (u *UI) View() string {
	w, _, bodyRows, leftW, rightW := u.layoutDims()
	var b strings.Builder
	write := func(s string) { b.WriteString(s); b.WriteString("\n") }

	q := u.currentQueue()
	status := ""
	if u.activeOps > 0 {
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
	title := " TSP MANAGER  v" + u.version
	titleW := runewidth.StringWidth(title)
	if titleW >= w {
		write(color("36;1", fit(title, w)))
	} else {
		write(color("36;1", title) + fit("   "+cleanTableCell(status), w-titleW))
	}
	write(strings.Repeat("─", w))

	write(color("33;1", fit(" QUEUES", leftW)) + " │ " + color("33;1", fit(" TASKS", rightW)))
	u.queueOffset = scrollWindow(u.queueOffset, u.selectedQueue, bodyRows, len(u.queues))
	if q != nil {
		u.taskOffset = scrollWindow(u.taskOffset, u.selectedTask, bodyRows-1, len(q.Tasks))
	} else {
		u.taskOffset = 0
	}

	var wrappedOverlay []string
	overlayVisible := bodyRows - 1
	if overlayVisible < 1 {
		overlayVisible = 1
	}
	if u.helpMode {
		wrappedOverlay = wrapLines(helpLines(), rightW)
		if maxOff := max(0, len(wrappedOverlay)-overlayVisible); u.helpOffset > maxOff {
			u.helpOffset = maxOff
		}
	} else if u.logMode {
		wrappedOverlay = wrapLines(u.logLines, rightW)
		if maxOff := max(0, len(wrappedOverlay)-overlayVisible); u.logOffset > maxOff {
			u.logOffset = maxOff
		}
	} else if u.infoMode {
		wrappedOverlay = wrapLines(u.infoLines, rightW)
		if maxOff := max(0, len(wrappedOverlay)-overlayVisible); u.infoOffset > maxOff {
			u.infoOffset = maxOff
		}
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
			name := cleanTableCell(item.Name)
			count := queueCountLabel(item)
			left = mark + state + " " + name + "  " + count
		}

		right := ""
		if u.helpMode {
			right = renderTopDownOverlay(row, " KEYBOARD SHORTCUTS  (Esc/? to close; ↑↓ scroll)", wrappedOverlay, u.helpOffset)
		} else if u.logMode {
			header := fmt.Sprintf(" LIVE OUTPUT  (Esc to close; ↑↓/PgUp/PgDn scroll; G bottom)  task #%d  %s", u.logTask, cleanTableCell(u.logPath))
			right = renderBottomUpOverlay(row, bodyRows, header, wrappedOverlay, u.logOffset, "(empty output)")
		} else if u.infoMode {
			right = renderTopDownOverlay(row, " TASK INFORMATION  (Esc to close; ↑↓/PgUp/PgDn scroll)", wrappedOverlay, u.infoOffset)
		} else if row == 0 {
			if q != nil {
				right = fmt.Sprintf("  %-4s %-9s %-5s %-10s %-18s %s", "ID", "STATE", "EXIT", "TIME", "OUTPUT", "COMMAND")
			} else {
				right = "No active queues discovered"
			}
		} else if q != nil && !q.Active && len(q.Tasks) == 0 {
			if row == 1 {
				errMsg := q.Error
				if errMsg == "" {
					errMsg = "queue is offline"
				}
				right = color("31", "  Queue offline: "+cleanTableCell(errMsg))
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
			command := cleanTableCell(t.Command)
			if command == "" {
				command = "(command unavailable)"
			}
			output := cleanTableCell(t.Output)
			if output == "" {
				output = "-"
			}
			right = fmt.Sprintf("%s%-4d %s %s %s %s %s",
				marker,
				t.ID,
				fit(state, 9),
				fit(cleanTableCell(t.ExitCode), 5),
				fit(formatTimes(t.Times), 10),
				fit(output, 18),
				command,
			)
		}
		write(fit(left, leftW) + " │ " + fit(right, rightW))
	}
	write(strings.Repeat("─", w))
	if u.modal == "prompt" {
		write(color("33;1", fitPrompt(" "+u.promptLabel+" ", u.promptValue, "▌", w)))
	} else if u.modal == "confirm" {
		hint := "y/Enter"
		if u.confirmKind == "remove" {
			hint = "r/y/Enter"
		}
		write(color("31;1", fit(" CONFIRM: "+u.confirmText+" ["+hint+" = yes, n/Esc = no]", w)))
	} else {
		write(fit(" Tab/←→ Pane  ↑↓ Move  Enter/i Details  l Logs  a Submit  n Add  d DelQ  k Kill  r Remove  C Clear  u Urgent  s Slots  R Refresh  ? Help  q Quit", w))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func renderTopDownOverlay(row int, title string, lines []string, offset int) string {
	if row == 0 {
		return color("33;1", title)
	}
	idx := offset + row - 1
	if idx >= 0 && idx < len(lines) {
		return lines[idx]
	}
	return ""
}

func renderBottomUpOverlay(row, bodyRows int, title string, lines []string, offset int, emptyLabel string) string {
	if row == 0 {
		return color("33;1", title)
	}
	if len(lines) == 0 {
		if row == 1 {
			return emptyLabel
		}
		return ""
	}
	visible := bodyRows - 1
	if visible < 1 {
		visible = 1
	}
	end := len(lines) - offset
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
		lineW := 6 + runewidth.StringWidth(cleanTableCell(q.Name)) + len(queueCountLabel(q)) + padding
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
	if maxOff := total - visible; offset > maxOff {
		offset = maxOff
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

func skipANSISeq(s string, i int) (int, bool) {
	if !strings.HasPrefix(s[i:], "\x1b[") {
		return i, false
	}
	j := i + 2
	for j < len(s) && s[j] != 'm' {
		j++
	}
	if j < len(s) {
		return j + 1, true
	}
	return i, false
}

func ansiDisplayWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if next, ok := skipANSISeq(s, i); ok {
			i = next
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := runewidth.RuneWidth(r)
		if rw > 0 {
			w += rw
		}
		i += size
	}
	return w
}

func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	totalW := ansiDisplayWidth(s)
	if totalW <= width {
		result := s + strings.Repeat(" ", width-totalW)
		if strings.Contains(result, "\x1b[") && !strings.HasSuffix(result, "\x1b[0m") {
			result += "\x1b[0m"
		}
		return result
	}

	var out strings.Builder
	visible := 0
	limit := width - 1
	for i := 0; i < len(s); {
		if next, ok := skipANSISeq(s, i); ok {
			out.WriteString(s[i:next])
			i = next
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := runewidth.RuneWidth(r)
		if rw < 0 {
			rw = 0
		}
		if visible+rw > limit {
			out.WriteRune('…')
			visible++
			break
		}
		out.WriteRune(r)
		visible += rw
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

// fitPrompt keeps the trailing portion of value and the cursor suffix visible
// when the typed command exceeds the terminal width.
func fitPrompt(prefix, value, suffix string, width int) string {
	full := prefix + value + suffix
	if runewidth.StringWidth(full) <= width {
		return fit(full, width)
	}
	avail := width - runewidth.StringWidth(prefix) - runewidth.StringWidth(suffix) - 1 // 1 for leading '…'
	if avail <= 0 {
		return fit(full, width)
	}
	runes := []rune(value)
	takenW := 0
	start := len(runes)
	for start > 0 {
		rw := runewidth.RuneWidth(runes[start-1])
		if rw < 0 {
			rw = 0
		}
		if takenW+rw > avail {
			break
		}
		takenW += rw
		start--
	}
	return fit(prefix+"…"+string(runes[start:])+suffix, width)
}

// wrapLines hard-wraps each line to width terminal display columns so long
// lines (such as task commands or logs) are shown in full instead of truncated.
// ANSI SGR sequences are passed through without counting toward the width.
func wrapLines(lines []string, width int) []string {
	if width < 1 {
		return lines
	}
	var out []string
	for _, s := range lines {
		if s == "" {
			out = append(out, "")
			continue
		}
		for {
			var b strings.Builder
			visible, i := 0, 0
			for i < len(s) {
				if next, ok := skipANSISeq(s, i); ok {
					b.WriteString(s[i:next])
					i = next
					continue
				}
				r, size := utf8.DecodeRuneInString(s[i:])
				rw := runewidth.RuneWidth(r)
				if rw < 0 {
					rw = 0
				}
				if visible > 0 && visible+rw > width {
					break
				}
				b.WriteRune(r)
				visible += rw
				i += size
				if visible >= width {
					break
				}
			}
			// Consume any trailing ANSI reset right at the wrap boundary.
			for i < len(s) {
				if next, ok := skipANSISeq(s, i); ok {
					b.WriteString(s[i:next])
					i = next
					continue
				}
				break
			}
			out = append(out, b.String())
			if s = s[i:]; s == "" {
				break
			}
		}
	}
	return out
}

func color(code, s string) string { return "\x1b[" + code + "m" + s + "\x1b[0m" }

func stateColor(s string) string {
	switch strings.ToLower(s) {
	case "running":
		return color("32;1", s)
	case "queued", "waiting", "allocating":
		return color("33", s)
	case "finished", "done":
		return color("36", s)
	case "skipped":
		return color("90", s)
	case "failed":
		return color("31;1", s)
	default:
		return s
	}
}
