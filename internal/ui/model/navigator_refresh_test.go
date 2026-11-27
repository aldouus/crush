package model

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

type overviewCountWorkspace struct {
	sendWorkspace
	loads atomic.Int32
}

func (w *overviewCountWorkspace) ListWorkspaceOverviewsLimit(context.Context, int) ([]proto.WorkspaceOverview, error) {
	w.loads.Add(1)
	return nil, nil
}

// runCmds executes cmd and every command batched inside it, giving each
// a short deadline so ticks and blocking subscriptions do not hang.
func runCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				runCmds(c)
			}
		}
	case <-time.After(200 * time.Millisecond):
	}
}

func TestNavigatorRefreshesOnRunNotification(t *testing.T) {
	t.Parallel()
	ws := &overviewCountWorkspace{sendWorkspace: sendWorkspace{readiness: func(context.Context) (bool, error) { return false, nil }}}
	u := newTestUI()
	u.com.Workspace = ws
	u.leftSidebarVisible = true

	_, cmd := u.Update(pubsub.Event[notify.Notification]{Payload: notify.Notification{SessionID: "other", Type: notify.TypeAgentFinished}})
	runCmds(cmd)
	require.EqualValues(t, 1, ws.loads.Load(), "a finished turn must refresh the navigator")

	_, cmd = u.Update(pubsub.Event[notify.RunComplete]{Payload: notify.RunComplete{SessionID: "other"}})
	runCmds(cmd)
	require.EqualValues(t, 1, ws.loads.Load(), "RunComplete must not load a second time")
}

func TestNavigatorRefreshSkippedWhenFocused(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.leftSidebarVisible = true
	u.focus = uiFocusLeftSidebar
	require.Nil(t, u.refreshNavigatorAfterRun())
	u.focus = uiFocusMain
	u.leftSidebarVisible = false
	require.Nil(t, u.refreshNavigatorAfterRun())
}
