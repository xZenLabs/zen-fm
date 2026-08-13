// Package platform centralizes the small amount of KOReader platform policy
// needed by the backend command.
package platform

import (
	"os"
	"path/filepath"
)

var ereaderRootCandidates = [...]string{"/mnt/us", "/mnt/onboard", "/mnt/ext1"}

func DefaultRoot() string {
	for _, candidate := range ereaderRootCandidates {
		if isDirectory(candidate) {
			return candidate
		}
	}
	if os.Getenv("ANDROID_ROOT") != "" || os.Getenv("ANDROID_DATA") != "" {
		for _, candidate := range []string{"/storage/emulated/0", "/sdcard"} {
			if isDirectory(candidate) {
				return candidate
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

func DefaultDataDir() string {
	if configured := os.Getenv("ZENFM_DATA_DIR"); configured != "" {
		return configured
	}
	if config, err := os.UserConfigDir(); err == nil && config != "" {
		return filepath.Join(config, "zenfm")
	}
	return filepath.Join(DefaultRoot(), ".zenfm")
}

func isDirectory(name string) bool {
	info, err := os.Stat(name)
	return err == nil && info.IsDir()
}
