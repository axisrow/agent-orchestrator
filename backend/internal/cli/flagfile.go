package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// resolveFlagText merges an inline flag value with its file variant, following
// the review submit convention (see #6021): agents pass long or
// shell-metacharacter-heavy text through a staged file with --<name>-file
// <path>, or pipe it through --<name>-file - (stdin); giving both the inline
// flag and its file variant at once is an error.
func resolveFlagText(cmd *cobra.Command, inline, fileArg, inlineFlag, fileFlag string) (string, error) {
	if strings.TrimSpace(fileArg) == "" {
		return inline, nil
	}
	if strings.TrimSpace(inline) != "" {
		return "", usageError{fmt.Errorf("use either --%s or --%s, not both", inlineFlag, fileFlag)}
	}
	if fileArg == "-" {
		raw, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", usageError{fmt.Errorf("--%s: read stdin: %w", fileFlag, err)}
		}
		return string(raw), nil
	}
	raw, err := os.ReadFile(fileArg)
	if err != nil {
		return "", usageError{fmt.Errorf("--%s: %w", fileFlag, err)}
	}
	return string(raw), nil
}
