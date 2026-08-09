//go:build !linux && !android && !darwin

package files

import "os"

func renameNoReplace(*os.Root, string, *os.Root, string) error {
	return errRenameNoReplaceUnsupported
}

func renameReplace(*os.Root, string, *os.Root, string) error {
	return errRenameNoReplaceUnsupported
}

func linkNoReplace(*os.Root, string, *os.Root, string) error {
	return errRenameNoReplaceUnsupported
}
