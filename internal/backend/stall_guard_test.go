package backend

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// A call that finished must never be reported as timed out, even when
// its goroutine is done before the caller waits for it.
func TestGuardedFinishedCallIsNotATimeout(t *testing.T) {
	t.Parallel()
	var g stallGuard
	for i := range 5000 {
		done := make(chan struct{})
		got, err := guarded(t.Context(), &g, "k", func(context.Context) (int, error) {
			defer close(done)
			return i, nil
		})
		require.NoError(t, err)
		require.Equal(t, i, got)
		<-done
		runtime.Gosched()
	}
}
