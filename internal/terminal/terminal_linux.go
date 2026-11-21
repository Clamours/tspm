//go:build linux

package terminal

import (
	"fmt"
	"os"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

type State struct {
	old syscall.Termios
}

type Size struct {
	Width  int
	Height int
}

type Key struct {
	Name string
	Rune rune
}

func EnterRaw() (*State, error) {
	fd := int(os.Stdin.Fd())
	var old syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&old))); errno != 0 {
		return nil, errno
	}
	raw := old
	raw.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	raw.Lflag &^= syscall.ECHO | syscall.ICANON
	raw.Cc[syscall.VMIN] = 0
	raw.Cc[syscall.VTIME] = 1
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&raw))); errno != 0 {
		return nil, errno
	}
	return &State{old: old}, nil
}

func (s *State) Restore() error {
	if s == nil {
		return nil
	}
	fd := int(os.Stdin.Fd())
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&s.old)))
	if errno != 0 {
		return errno
	}
	return nil
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

func readStdin(b []byte) (int, error) {
	// os.Stdin.Read translates a successful 0-byte read into io.EOF,
	// which breaks VMIN=0/VTIME>0 polling reads on a terminal.
	return syscall.Read(int(os.Stdin.Fd()), b)
}

func ReadKey() (Key, error) {
	var b [1]byte
	n, err := readStdin(b[:])
	if err != nil {
		return Key{}, err
	}
	if n == 0 {
		return Key{Name: "timeout"}, nil
	}
	c := b[0]
	switch c {
	case 3:
		return Key{Name: "ctrl-c"}, nil
	case 9:
		return Key{Name: "tab"}, nil
	case 10, 13:
		return Key{Name: "enter"}, nil
	case 127, 8:
		return Key{Name: "backspace"}, nil
	case 27:
		var seq [2]byte
		n2, _ := readStdin(seq[:1])
		if n2 == 0 {
			return Key{Name: "esc"}, nil
		}
		if seq[0] == '[' || seq[0] == 'O' {
			n3, _ := readStdin(seq[1:2])
			if n3 == 0 {
				return Key{Name: "esc"}, nil
			}
			switch seq[1] {
			case 'A':
				return Key{Name: "up"}, nil
			case 'B':
				return Key{Name: "down"}, nil
			case 'C':
				return Key{Name: "right"}, nil
			case 'D':
				return Key{Name: "left"}, nil
			case 'H':
				return Key{Name: "home"}, nil
			case 'F':
				return Key{Name: "end"}, nil
			}
		}
		return Key{Name: "esc"}, nil
	default:
		if c < utf8.RuneSelf {
			return Key{Name: "rune", Rune: rune(c)}, nil
		}
		size := 0
		switch {
		case c&0xE0 == 0xC0:
			size = 2
		case c&0xF0 == 0xE0:
			size = 3
		case c&0xF8 == 0xF0:
			size = 4
		default:
			return Key{Name: "rune", Rune: utf8.RuneError}, nil
		}
		buf := make([]byte, size)
		buf[0] = c
		for i := 1; i < size; {
			n, readErr := readStdin(buf[i:])
			if readErr != nil {
				return Key{}, readErr
			}
			if n == 0 {
				return Key{Name: "rune", Rune: utf8.RuneError}, nil
			}
			i += n
		}
		r, _ := utf8.DecodeRune(buf)
		return Key{Name: "rune", Rune: r}, nil
	}
}

func IsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

func EnterScreen() {
	fmt.Print("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H")
}

func LeaveScreen() {
	fmt.Print("\x1b[0m\x1b[?25h\x1b[?1049l")
}
