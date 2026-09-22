package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Full print path: res in daemon order (heaviest NOT first) must come out
// heaviest-first from writeProcessInventory.
func TestWriteProcessInventory_SortsSessionsDesc(t *testing.T) {
	res := processInventoryResponse{
		Totals: processTotalsDTO{SessionsCount: 3, SessionsRSSBytes: 660 * 1024 * 1024},
		Trees: []processTreeDTO{
			{SessionID: "small", RootPID: 3, RSSBytes: 31 * 1024 * 1024, State: "owned", Attached: true},
			{SessionID: "huge", RootPID: 1, RSSBytes: 374 * 1024 * 1024, State: "owned", Attached: true},
			{SessionID: "mid", RootPID: 2, RSSBytes: 306 * 1024 * 1024, State: "owned", Attached: true},
		},
	}
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := writeProcessInventory(cmd, res); err != nil {
		t.Fatalf("writeProcessInventory: %v", err)
	}
	text := out.String()
	iHuge := strings.Index(text, "huge")
	iMid := strings.Index(text, "mid")
	iSmall := strings.Index(text, "small")
	if !(0 <= iHuge && iHuge < iMid && iMid < iSmall) {
		t.Fatalf("row order wrong (huge=%d mid=%d small=%d):\n%s", iHuge, iMid, iSmall, text)
	}
}
