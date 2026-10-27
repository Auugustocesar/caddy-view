//go:build windows

package ingest

import (
	"fmt"
	"os"
	"syscall"
)

// fileID identifies a file by volume serial number and file index, so a new file created at the same path is told
// apart from the old one. It is empty when the file cannot be opened.
func fileID(path string, info os.FileInfo) string {
	if info == nil {
		return ""
	}
	// os.Open shares the file for reading, writing and deleting, so a log roller is never blocked by this.
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var data syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &data); err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d:%d", data.VolumeSerialNumber, data.FileIndexHigh, data.FileIndexLow)
}
