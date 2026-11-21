package spooler

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/Clamours/tspm/internal/model"
)

var timePattern = regexp.MustCompile(`^(?:\?|\d+(?:\.\d+)?(?:/\d+(?:\.\d+)?){0,2})$`)

type lineToken struct {
	text  string
	start int
	end   int
}

func tokenizeLine(line string) []lineToken {
	var tokens []lineToken
	i := 0
	for i < len(line) {
		for i < len(line) && unicode.IsSpace(rune(line[i])) {
			i++
		}
		if i >= len(line) {
			break
		}
		start := i
		for i < len(line) && !unicode.IsSpace(rune(line[i])) {
			i++
		}
		tokens = append(tokens, lineToken{text: line[start:i], start: start, end: i})
	}
	return tokens
}

func isInt(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

func ParseTaskList(output string) ([]model.Task, error) {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	headerIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "ID") && strings.Contains(line, "State") && strings.Contains(line, "Command") {
			headerIndex = i
			break
		}
	}
	if headerIndex < 0 {
		var tasks []model.Task
		for _, line := range lines {
			if t, ok := parseFallbackLine(line); ok {
				tasks = append(tasks, t)
			}
		}
		if len(tasks) == 0 && strings.TrimSpace(output) != "" {
			return nil, fmt.Errorf("unrecognized tsp -l output (header not found)")
		}
		return tasks, nil
	}

	header := lines[headerIndex]
	commandCol := strings.Index(header, "Command")
	outputCol := strings.Index(header, "Output")
	errorCol := strings.Index(header, "E-Level")
	timesCol := strings.Index(header, "Times")
	hasOutputColumn := outputCol >= 0 && outputCol < commandCol
	hasExitColumn := errorCol >= 0 && errorCol < commandCol
	hasTimesColumn := timesCol >= 0 && timesCol < commandCol

	var tasks []model.Task
	for _, line := range lines[headerIndex+1:] {
		tokens := tokenizeLine(line)
		if len(tokens) < 2 {
			continue
		}
		id, err := strconv.Atoi(tokens[0].text)
		if err != nil {
			continue
		}
		t := model.Task{ID: id, State: strings.ToLower(tokens[1].text)}
		idx := 2

		// The table's columns are padded for humans, but empty cells disappear
		// when split into fields. Parse optional cells by their values while
		// using column offsets to preserve spaces in paths and commands.
		if hasOutputColumn && idx < len(tokens) {
			outStart := tokens[idx].start
			outEnd := tokens[idx].end
			// If an output path contains spaces and stays within the Output column
			// (before E-Level), merge adjacent tokens belonging to that cell.
			if errorCol > outputCol && tokens[idx].text != "(file)" && tokens[idx].text != "-" && tokens[idx].start < errorCol {
				for idx+1 < len(tokens) && tokens[idx+1].start < errorCol {
					idx++
					outEnd = tokens[idx].end
				}
			}
			t.Output = strings.TrimSpace(line[outStart:outEnd])
			if t.Output == "(file)" || t.Output == "-" {
				t.Output = ""
			}
			idx++
		}
		if hasExitColumn && idx < len(tokens) {
			f := tokens[idx].text
			hasExitState := t.State == "finished" || t.State == "done" || t.State == "failed" || t.State == "skipped"
			if (hasExitState || f == "?") && (f == "?" || isInt(f)) {
				t.ExitCode = f
				idx++
			}
		}
		if hasTimesColumn && idx < len(tokens) && timePattern.MatchString(tokens[idx].text) {
			// For running/queued tasks, a command can itself begin with a number.
			// Treat a plain numeric token as a time only for terminal task states.
			f := tokens[idx].text
			isTerminalState := t.State == "finished" || t.State == "done" || t.State == "failed"
			if f == "?" || strings.Contains(f, "/") || isTerminalState {
				t.Times = f
				idx++
			}
		}
		if idx < len(tokens) {
			t.Command = strings.TrimSpace(line[tokens[idx].start:])
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func parseFallbackLine(line string) (model.Task, bool) {
	tokens := tokenizeLine(line)
	if len(tokens) < 2 {
		return model.Task{}, false
	}
	id, err := strconv.Atoi(tokens[0].text)
	if err != nil {
		return model.Task{}, false
	}
	t := model.Task{ID: id, State: strings.ToLower(tokens[1].text)}
	if len(tokens) > 2 {
		t.Command = strings.TrimSpace(line[tokens[2].start:])
	}
	return t, true
}
