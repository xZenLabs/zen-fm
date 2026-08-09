//go:build !linux && !android && !darwin

package files

import "os"

func fileDeviceID(os.FileInfo) (uint64, bool) { return 0, false }

func fileObjectID(os.FileInfo) (objectID, bool) { return objectID{}, false }
