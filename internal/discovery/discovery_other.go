//go:build windows

package discovery

import "os"

func ownedByCurrentUser(st os.FileInfo) bool {
	return true
}
