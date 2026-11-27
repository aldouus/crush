//go:build !windows

package cmd

import (
	"os"
	"os/signal"
	"syscall"
)

func addSignals(sigs []os.Signal) []os.Signal {
	return append(sigs, syscall.SIGTERM)
}

// ignoreTerminalHangup keeps the server alive when its terminal closes.
func ignoreTerminalHangup() {
	signal.Ignore(syscall.SIGHUP)
}
