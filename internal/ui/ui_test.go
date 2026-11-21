package ui

import (
	"testing"

	"github.com/tspm-project/tsp-manager/internal/model"
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
