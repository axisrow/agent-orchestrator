//go:build darwin

package conpty

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

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

// newPtyInputProbe resolves the slave name from the master (TIOCPTYGNAME) and
// opens it detached: O_NOCTTY keeps it from becoming this process's
// controlling terminal.
func newPtyInputProbe(master *os.File) (*ptyInputProbe, error) {
	var name [128]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(),
		uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0]))) //nolint:gosec // ioctl out buffer, kernel-written
	if errno != 0 {
		return nil, fmt.Errorf("TIOCPTYGNAME: %w", errno)
	}
	slave, err := os.OpenFile(unix.ByteSliceToString(name[:]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("open pty slave: %w", err)
	}
	return &ptyInputProbe{slave: slave}, nil
}

// fionread is the tty input-queue count ioctl. stdlib syscall does not export
// it on darwin; the value is the classic TIOCINQ request.
const fionread = 0x4004667f

// PendingInput returns the unread byte count of the tty input queue — exactly
// the queue the kernel clamps at ~1 KiB on macOS.
func (p *ptyInputProbe) PendingInput() (int, error) {
	n, err := unix.IoctlGetInt(int(p.slave.Fd()), fionread)
	if err != nil {
		return 0, fmt.Errorf("FIONREAD on pty slave: %w", err)
	}
	return n, nil
}

// FlushInput discards queued unread input, the moral TCIFLUSH: macOS expresses
// it as TIOCFLUSH with the FREAD selector (1).
func (p *ptyInputProbe) FlushInput() error {
	if err := unix.IoctlSetPointerInt(int(p.slave.Fd()), unix.TIOCFLUSH, 1); err != nil {
		return fmt.Errorf("TIOCFLUSH on pty slave: %w", err)
	}
	return nil
}

// Close releases the probe's slave fd. Called with the PTY's Close.
func (p *ptyInputProbe) Close() error { return p.slave.Close() }
