package tools

import (
	"context"
	"time"
)

// BackgroundOnRequestAfter is how long a tool call must have been running
// before a background request (a queued /btw aside) moves it to a
// background job so the turn can continue and fold the aside in.
const BackgroundOnRequestAfter = 10 * time.Second

type backgroundRequestKey struct{}

// WithBackgroundRequest attaches a channel that is closed when the caller
// would like a long-running tool call to move itself to the background.
func WithBackgroundRequest(ctx context.Context, ch <-chan struct{}) context.Context {
	return context.WithValue(ctx, backgroundRequestKey{}, ch)
}

// BackgroundRequest returns the background request channel from ctx, or
// nil when none is attached (a nil channel never fires in a select).
func BackgroundRequest(ctx context.Context) <-chan struct{} {
	ch, _ := ctx.Value(backgroundRequestKey{}).(<-chan struct{})
	return ch
}
