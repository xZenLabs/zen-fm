//go:build !linux && !android && !darwin

package files

import "errors"

func filesystemUsage(string) (Usage, error) {
	return Usage{}, errors.New("filesystem capacity is unsupported on this platform")
}
