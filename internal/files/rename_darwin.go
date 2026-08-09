//go:build darwin

package files

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameNoReplace(sourceParent *os.Root, source string, destinationParent *os.Root, destination string) error {
	sourceDirectory, destinationDirectory, err := openDirectoryPair(sourceParent, destinationParent)
	if err != nil {
		return err
	}
	defer sourceDirectory.Close()
	defer destinationDirectory.Close()
	return unix.RenameatxNp(int(sourceDirectory.Fd()), source, int(destinationDirectory.Fd()), destination, unix.RENAME_EXCL)
}

func renameReplace(sourceParent *os.Root, source string, destinationParent *os.Root, destination string) error {
	sourceDirectory, destinationDirectory, err := openDirectoryPair(sourceParent, destinationParent)
	if err != nil {
		return err
	}
	defer sourceDirectory.Close()
	defer destinationDirectory.Close()
	return unix.Renameat(int(sourceDirectory.Fd()), source, int(destinationDirectory.Fd()), destination)
}

func linkNoReplace(sourceParent *os.Root, source string, destinationParent *os.Root, destination string) error {
	sourceDirectory, destinationDirectory, err := openDirectoryPair(sourceParent, destinationParent)
	if err != nil {
		return err
	}
	defer sourceDirectory.Close()
	defer destinationDirectory.Close()
	return unix.Linkat(int(sourceDirectory.Fd()), source, int(destinationDirectory.Fd()), destination, 0)
}
