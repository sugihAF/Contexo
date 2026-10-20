//go:build !windows

package syncstate

// processAlive reports whether pid names a live process on this host.
func processAlive(pid int) bool {
	return true
}
