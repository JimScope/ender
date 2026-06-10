//go:build !linux

package main

// configureSerialPort is a no-op on non-Linux platforms. Serial modems are
// expected to run on Linux hosts; elsewhere the port must already be in raw
// mode (e.g. via stty) for AT parsing to work.
func configureSerialPort(path string) error {
	return nil
}
