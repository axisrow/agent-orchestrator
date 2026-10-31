//go:build linux

package conpty

import (
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// ptyInputProbe observes the PTY's input queue from the slave side: FIONREAD
// on the master measures the wrong direction (the output side), so the probe
// holds its own slave fd for the life of the PTY.
//
// The extra open fd keeps the slave alive after the child exits, which turns
// the master's post-exit read from the usual EIO into a quiet block. Nothing
// depends on that EIO — child exit is detected via process wait, not the
// master — and the fd (and the block) die with the PTY on Close.
type ptyInputProbe struct {
	slave *os.File
}

// newPtyInputProbe resolves the slave path from the master (TIOCGPTN ->
// /dev/pts/N) and opens it detached: O_NOCTTY keeps it from becoming this
// process's controlling terminal.
func newPtyInputProbe(master *os.File) (*ptyInputProbe, error) {
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		return nil, fmt.Errorf("TIOCGPTN: %w", err)
	}
	slave, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("open pty slave: %w", err)
	}
	return &ptyInputProbe{slave: slave}, nil
}

// fionread is the tty input-queue count ioctl (TIOCINQ); x/sys/unix does not
// define it.
const fionread = 0x541b

// PendingInput returns the unread byte count of the tty input queue (TIOCINQ).
func (p *ptyInputProbe) PendingInput() (int, error) {
	n, err := unix.IoctlGetInt(int(p.slave.Fd()), fionread)
	if err != nil {
		return 0, fmt.Errorf("FIONREAD on pty slave: %w", err)
	}
	return n, nil
}

// FlushInput discards queued unread input. Linux takes the TCFLSH selector by
// value (a pointer arg is EINVAL).
func (p *ptyInputProbe) FlushInput() error {
	if err := unix.IoctlSetInt(int(p.slave.Fd()), unix.TCFLSH, unix.TCIFLUSH); err != nil {
		return fmt.Errorf("TCIFLUSH on pty slave: %w", err)
	}
	return nil
}

// Close releases the probe's slave fd. Called with the PTY's Close.
func (p *ptyInputProbe) Close() error { return p.slave.Close() }
