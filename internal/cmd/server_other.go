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
//
// It catches SIGHUP and drops it rather than calling signal.Ignore:
// SIG_IGN survives fork/exec, so every child the server starts (bash
// tool, LSPs, MCP servers, hooks) would ignore SIGHUP too. A caught
// signal is reset to its default action on exec.
func ignoreTerminalHangup() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	go func() {
		for range ch {
		}
	}()
}
