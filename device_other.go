//go:build !unix

package trash

import "os"

// device has no portable equivalent here; everything is treated as one
// filesystem and trashed into the home trash.
func device(os.FileInfo) uint64 { return 0 }
