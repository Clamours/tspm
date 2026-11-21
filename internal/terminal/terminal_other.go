//go:build !linux

package terminal

import (
	"errors"
	"os"
)

type State struct{}
type Size struct{ Width, Height int }
type Key struct {
	Name string
	Rune rune
}

func EnterRaw() (*State, error) {
	return nil, errors.New("interactive TUI currently supports Linux only")
}
func (s *State) Restore() error { return nil }
func GetSize() Size             { return Size{Width: 100, Height: 30} }
func ReadKey() (Key, error) {
	var b [1]byte
	n, err := os.Stdin.Read(b[:])
	if err != nil {
		return Key{}, err
	}
	if n == 0 {
		return Key{Name: "timeout"}, nil
	}
	return Key{Name: "rune", Rune: rune(b[0])}, nil
}
func IsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}
func EnterScreen() {}
func LeaveScreen() {}
