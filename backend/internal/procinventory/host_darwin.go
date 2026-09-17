//go:build darwin

package procinventory

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// freeRootDiskBytes returns unprivileged free bytes on the root volume — the
// honest ceiling macOS swap can grow into. Package var so tests can override.
var freeRootDiskBytes = func() (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return 0, fmt.Errorf("statfs /: %w", err)
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

// runCommand spawns name with a C-pinned locale: sysctl localizes its
// swapusage decimals (comma under ru_RU), and the parsers accept both — the
// pin just makes the common path canonical.
func runCommand(ctx context.Context, args ...string) (string, error) {
	cmd := aoprocess.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// defaultHostStats snapshots host memory: physical total and swap in one
// sysctl spawn, page kinds from vm_stat, the swap growth ceiling from the
// root volume's free space, and a best-effort OS free-memory estimate.
func defaultHostStats(ctx context.Context) (HostStats, error) {
	sysctlOut, err := runCommand(ctx, "sysctl", "-n", "hw.memsize", "vm.swapusage")
	if err != nil {
		return HostStats{}, fmt.Errorf("sysctl hw.memsize vm.swapusage: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(sysctlOut), "\n")
	if len(lines) < 2 {
		return HostStats{}, fmt.Errorf("sysctl hw.memsize vm.swapusage: expected two lines, got %q", sysctlOut)
	}
	total, err := strconv.ParseInt(strings.TrimSpace(lines[0]), 10, 64)
	if err != nil {
		return HostStats{}, fmt.Errorf("sysctl hw.memsize: %w", err)
	}
	swap, err := ParseSwapUsage(lines[1])
	if err != nil {
		return HostStats{}, err
	}

	vmOut, err := runCommand(ctx, "vm_stat")
	if err != nil {
		return HostStats{}, fmt.Errorf("vm_stat: %w", err)
	}
	vm, err := ParseVmStat(vmOut)
	if err != nil {
		return HostStats{}, err
	}

	freeDisk, err := freeRootDiskBytes()
	if err != nil {
		return HostStats{}, err
	}

	var pressure *int
	if pressureOut, pressureErr := runCommand(ctx, "memory_pressure"); pressureErr == nil {
		if percent, ok := ParsePressureFreePercent(pressureOut); ok {
			pressure = &percent
		}
	}

	return composeDarwinHostStats(total, swap, vm, freeDisk, pressure), nil
}
