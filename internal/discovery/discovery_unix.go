//go:build !windows

package discovery

import (
	"os"
	"syscall"
)

func ownedByCurrentUser(st os.FileInfo) bool {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	return int(sys.Uid) == os.Geteuid()
}
