//go:build !unix && !windows

package ingest

import "os"

// fileID has no file identity to offer on this platform; rotation is then noticed by the fingerprint and the size.
func fileID(string, os.FileInfo) string { return "" }
