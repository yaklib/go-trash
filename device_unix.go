//go:build unix

package trash

import (
	"os"
	"syscall"
)

// device returns the filesystem device id of fi.
func device(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}
