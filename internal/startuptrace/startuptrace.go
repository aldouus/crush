// Package startuptrace records startup phase timestamps for benchmarking.
//
// It is disabled unless CRUSH_STARTUP_TRACE names a file. Each phase is
// appended once per process as "<pid> <phase> <unix-nanos>\n".
package startuptrace

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	path = os.Getenv("CRUSH_STARTUP_TRACE")
	mu   sync.Mutex
	seen = map[string]bool{}
)

// Enabled reports whether startup tracing is on.
func Enabled() bool { return path != "" }

// Mark records the first occurrence of phase. It is a no-op when tracing
// is disabled.
func Mark(phase string) {
	if path == "" {
		return
	}
	now := time.Now().UnixNano()
	mu.Lock()
	defer mu.Unlock()
	if seen[phase] {
		return
	}
	seen[phase] = true
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%d %s %d\n", os.Getpid(), phase, now)
}
