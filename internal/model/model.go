package model

import "time"

type Task struct {
	ID        int       `json:"id"`
	State     string    `json:"state"`
	Output    string    `json:"output,omitempty"`
	ExitCode  string    `json:"exit_code,omitempty"`
	Times     string    `json:"times,omitempty"`
	Command   string    `json:"command,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type Queue struct {
	Name      string    `json:"name"`
	Socket    string    `json:"socket"`
	Source    string    `json:"source"`
	Active    bool      `json:"active"`
	Slots     int       `json:"slots"`
	Tasks     []Task    `json:"tasks"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

func (q Queue) RunningCount() int {
	n := 0
	for _, t := range q.Tasks {
		if t.State == "running" {
			n++
		}
	}
	return n
}

func (q Queue) PendingCount() int {
	n := 0
	for _, t := range q.Tasks {
		if t.State == "queued" || t.State == "waiting" {
			n++
		}
	}
	return n
}

func (q Queue) FinishedCount() int {
	n := 0
	for _, t := range q.Tasks {
		if t.State == "finished" || t.State == "done" {
			n++
		}
	}
	return n
}

func (q Queue) FailedCount() int {
	n := 0
	for _, t := range q.Tasks {
		if t.State == "failed" || (t.State == "finished" && t.ExitCode != "" && t.ExitCode != "0" && t.ExitCode != "?") {
			n++
		}
	}
	return n
}
