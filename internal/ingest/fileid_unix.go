//go:build unix

package ingest

import (
	"fmt"
	"os"
	"syscall"
)

// fileID identifies a file by device and inode, so a new file created at the same path is told apart from the old
// one. It is empty when the system gives no identity.
func fileID(_ string, info os.FileInfo) string {
	if info == nil {
		return ""
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	// The field types differ per platform, hence the conversions.
	return fmt.Sprintf("%d:%d", uint64(st.Dev), uint64(st.Ino))
}
