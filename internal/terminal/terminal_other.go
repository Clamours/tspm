//go:build !linux

package terminal

import (
	"os"

	"github.com/mattn/go-isatty"
)

type Size struct {
	Width  int
	Height int
}

type Key struct {
	Name  string
	Rune  rune
	Runes []rune
}

func GetSize() Size {
	return Size{Width: 100, Height: 30}
}

func IsTerminal() bool {
	fd := os.Stdin.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}
