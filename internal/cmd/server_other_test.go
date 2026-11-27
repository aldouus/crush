//go:build !windows

package cmd

import (
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIgnoreTerminalHangupNotInherited checks that keeping the server
// alive on SIGHUP does not leave its children ignoring SIGHUP.
func TestIgnoreTerminalHangupNotInherited(t *testing.T) {
	ignoreTerminalHangup()
	t.Cleanup(func() { signal.Reset(syscall.SIGHUP) })

	// A child that inherited SIG_IGN survives its own SIGHUP and prints.
	out, _ := exec.CommandContext(t.Context(), "sh", "-c", "kill -HUP $$; echo survived").Output()
	require.NotContains(t, strings.TrimSpace(string(out)), "survived")
}
