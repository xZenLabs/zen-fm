//go:build !linux && !android

package files

import "os"

func pseudoFilesystemFile(*os.File) bool { return false }
