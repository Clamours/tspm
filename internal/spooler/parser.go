package spooler

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/tspm-project/tsp-manager/internal/model"
)

var timePattern = regexp.MustCompile(`^(?:\?|\d+(?:\.\d+)?(?:/\d+(?:\.\d+)?){0,2})$`)

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
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		id, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		t := model.Task{ID: id, State: strings.ToLower(fields[1])}
		idx := 2

		// The table's columns are padded for humans, but empty cells disappear
		// when split into fields. Parse the optional cells by their values instead
		// of slicing at fixed byte offsets (which varies across tsp forks).
		if hasOutputColumn && idx < len(fields) {
			t.Output = fields[idx]
			if t.Output == "(file)" || t.Output == "-" {
				t.Output = ""
			}
			idx++
		}
		if hasExitColumn && idx < len(fields) {
			f := fields[idx]
			if t.State == "finished" || f == "?" {
				t.ExitCode = f
				idx++
			}
		}
		if hasTimesColumn && idx < len(fields) && timePattern.MatchString(fields[idx]) {
			// For running/queued tasks, a command can itself begin with a number.
			// Treat a plain numeric token as a time only for finished tasks.
			f := fields[idx]
			if f == "?" || strings.Contains(f, "/") || t.State == "finished" {
				t.Times = f
				idx++
			}
		}
		if idx < len(fields) {
			t.Command = strings.Join(fields[idx:], " ")
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func parseFallbackLine(line string) (model.Task, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return model.Task{}, false
	}
	id, err := strconv.Atoi(fields[0])
	if err != nil {
		return model.Task{}, false
	}
	t := model.Task{ID: id, State: strings.ToLower(fields[1])}
	if len(fields) > 2 {
		t.Command = strings.Join(fields[2:], " ")
	}
	return t, true
}
