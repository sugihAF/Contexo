package updater

import (
	"bytes"

	"github.com/minio/selfupdate"
)

// Apply atomically replaces the currently running executable with newBinary.
// On Windows this performs the rename dance required to swap a running .exe;
// on failure selfupdate rolls the old binary back into place.
func Apply(newBinary []byte) error {
	return selfupdate.Apply(bytes.NewReader(newBinary), selfupdate.Options{})
}
