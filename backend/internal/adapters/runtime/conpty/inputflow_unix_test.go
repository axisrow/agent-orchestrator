//go:build darwin || linux

package conpty

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The #177 transport guarantee: a report batch bigger than the kernel's
// ~1 KiB unread-input clamp reaches a pty child whole under flow control, or
// the write fails loudly — never a silent ~1022-byte cut.

// startRealPTYHost serves a host around a real kernel pty running shellScript.
func startRealPTYHost(t *testing.T, shellScript string) (*host, string, func()) {
	t.Helper()
	dir := t.TempDir()
	conn, err := newConPTY(dir, "sh", []string{"-c", shellScript}, initialConPTYColumns, initialConPTYRows)
	if err != nil {
		t.Fatalf("newConPTY: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	h := newHost(ServeConfig{
		SessionID: "inputflow-test",
		Listener:  ln,
		PTY:       conn,
		Ring:      NewRing(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = h.run(ctx) }()
	cleanup := func() {
		cancel()
		_ = ln.Close()
		_ = conn.Close()
	}
	return h, dir, cleanup
}

// TestSyncInputDeliversWholeBeyondKernelClamp sends a 4 KiB batch to a child
// that reads nothing for a second — the diagnostic's loss window, long enough
// for the old unpaced write to lose everything past the clamp — and asserts
// the child receives every byte plus the trailing Enter.
func TestSyncInputDeliversWholeBeyondKernelClamp(t *testing.T) {
	// Raw mode mirrors the claude-code TUI recipient; the child reads exactly
	// 4097 bytes (batch + Enter) after the non-reading window, so the Enter is
	// consumed before the child exits and cannot race its own exit ack.
	h, dir, cleanup := startRealPTYHost(t, "stty raw -echo; sleep 1; head -c 4097 > out.txt")
	defer cleanup()
	if _, ok := h.cfg.PTY.(inputFlowController); !ok {
		t.Fatal("real unix pty must implement inputFlowController")
	}

	message := strings.Repeat("abcdefgh", 512) // 4096 B, far past the ~1022 clamp
	want := message + "\r"
	if err := clientSendMessage(lnAddr(h), message); err != nil {
		t.Fatalf("clientSendMessage: %v", err)
	}

	out := filepath.Join(dir, "out.txt")
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(out)
		if err == nil && len(data) == len(want) {
			if string(data) != want {
				t.Fatalf("recipient got %d bytes but content differs: prefix %q", len(data), string(data[:64]))
			}
			return
		}
		if time.Now().After(deadline) {
			got := ""
			if err == nil {
				got = string(data)
			}
			t.Fatalf("recipient never received the whole batch: file has %d/%d bytes (tail %q)", len(got), len(want), suffix(got))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestSyncInputFailsLoudWhenRecipientNeverReads: a child that never reads
// stdin must fail the delivery loudly and leave nothing queued — no partial
// batch for the recipient to submit later. Raw mode mirrors the TUI recipient
// and makes every queued byte visible to the probe.
func TestSyncInputFailsLoudWhenRecipientNeverReads(t *testing.T) {
	oldTimeout := inputFlowTimeout
	inputFlowTimeout = 300 * time.Millisecond
	defer func() { inputFlowTimeout = oldTimeout }()

	h, _, cleanup := startRealPTYHost(t, "stty raw -echo; sleep 30")
	defer cleanup()

	flow := h.cfg.PTY.(inputFlowController)
	message := strings.Repeat("x", 4096)
	start := time.Now()
	err := clientSendMessage(lnAddr(h), message)
	if err == nil {
		t.Fatal("delivery to a non-reading recipient must fail loudly")
	}
	if !strings.Contains(err.Error(), "did not read input") {
		t.Fatalf("error should name the stall, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("failure took %s; the host must ack the stall, not strand the sender", elapsed)
	}
	if pending, err := flow.PendingInput(); err != nil || pending != 0 {
		t.Fatalf("queued input must be flushed after the failure, pending=%d err=%v", pending, err)
	}
}

// TestSyncInputWritesThroughWithoutFlowObserver covers PTYs without the
// inputFlowController capability (windows ConPTY, deferred pty): sync input
// degrades to the historical single write and still acks success. The fake
// PTY's io.Pipe is synchronous, so the reads run alongside the send.
func TestSyncInputWritesThroughWithoutFlowObserver(t *testing.T) {
	f := startServe(t, 200)
	defer f.cancel()

	type readResult struct {
		data string
		err  error
	}
	readC := make(chan readResult, 2)
	go func() {
		buf := make([]byte, 32)
		for i := 0; i < 2; i++ {
			n, err := f.pty.ReadInput(buf)
			readC <- readResult{string(buf[:n]), err}
		}
	}()
	if err := clientSendMessage(f.addr, "hello"); err != nil {
		t.Fatalf("clientSendMessage: %v", err)
	}
	for i, want := range []string{"hello", "\r"} {
		select {
		case got := <-readC:
			if got.err != nil || got.data != want {
				t.Fatalf("write-through %d = %q, err %v; want %q", i, got.data, got.err, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for write-through %d", i)
		}
	}
}

func lnAddr(h *host) string {
	return h.cfg.Listener.Addr().String()
}

func suffix(s string) string {
	if len(s) <= 48 {
		return s
	}
	return s[len(s)-48:]
}
