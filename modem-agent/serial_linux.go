//go:build linux

package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// configureSerialPort puts a tty into raw 115200 8N1 mode before the at
// library opens it. The library uses a bare os.OpenFile and never touches
// termios, so a port left in canonical mode (echo on, line buffering)
// breaks AT response parsing. Settings are per-device on Linux, so they
// persist for the subsequent open.
func configureSerialPort(path string) error {
	// O_NONBLOCK so the open doesn't hang waiting for DCD on real UARTs.
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer unix.Close(fd)

	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return fmt.Errorf("get termios %s: %w", path, err)
	}

	// Raw mode (equivalent to cfmakeraw): no line editing, no echo, no
	// signal chars, no CR/LF translation, 8-bit clean in both directions.
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB
	t.Cflag |= unix.CS8

	// Local line (ignore modem control lines), enable receiver and don't
	// drop DTR on close — a HUP would reset some USB modems between the
	// probe open and the real open.
	t.Cflag |= unix.CLOCAL | unix.CREAD
	t.Cflag &^= unix.HUPCL

	// 115200 baud. USB CDC-ACM modems ignore it; real UARTs need it.
	t.Cflag &^= unix.CBAUD
	t.Cflag |= unix.B115200
	t.Ispeed = unix.B115200
	t.Ospeed = unix.B115200

	// Blocking read of at least one byte, no inter-byte timer. The at
	// library applies its own deadlines via File.SetDeadline.
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0

	if err := unix.IoctlSetTermios(fd, unix.TCSETS, t); err != nil {
		return fmt.Errorf("set termios %s: %w", path, err)
	}
	return nil
}
