//go:build linux

package terminal

import (
	"os"
	"syscall"
	"unsafe"

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
	var ws struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(os.Stdout.Fd()), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 || ws.Col == 0 || ws.Row == 0 {
		return Size{Width: 100, Height: 30}
	}
	return Size{Width: int(ws.Col), Height: int(ws.Row)}
}

func IsTerminal() bool {
	fd := os.Stdin.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}
