package model

import (
	"context"
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/startuptrace"
	"github.com/taigrr/crush/internal/workspace"
)

// Background loading paces itself so it never competes with startup or
// input: it starts a moment after the first overview lands, pages up to
// bgConcurrency workspaces at once (the current one first), and
// re-projects the sidebar rows at most every bgApplyInterval. Pages are
// fetched off the event loop, so input is handled between them.
const (
	bgStartDelay    = 250 * time.Millisecond
	bgApplyInterval = 300 * time.Millisecond
	bgPageSize      = 2000
	bgConcurrency   = 2
)

// backgroundLoader is the state of the progressive session loader. gen
// invalidates in-flight pages after a reset.
type backgroundLoader struct {
	gen       uint64
	ctx       context.Context
	cancel    context.CancelFunc
	started   bool
	done      bool
	queue     []string
	inflight  int
	lastApply time.Time
	pending   bool
	lastCost  time.Duration
	maxApply  time.Duration
}

type (
	bgLoadStartMsg struct{ gen uint64 }
	bgPageMsg      struct {
		gen  uint64
		root string
		ov   proto.WorkspaceOverview
		err  error
	}
)

// complete reports whether every bounded workspace has been paged in.
func (l *backgroundLoader) complete() bool { return l.started && l.done }

// resetBackgroundLoad cancels any in-flight paging and forgets what was
// loaded; the next overview restarts it.
func (m *UI) resetBackgroundLoad() {
	if m.bgLoad.cancel != nil {
		m.bgLoad.cancel()
	}
	m.bgLoad = backgroundLoader{gen: m.bgLoad.gen + 1}
	if m.leftSidebar != nil {
		m.leftSidebar.ClearBackgroundSessions()
	}
}

// maybeStartBackgroundLoad schedules the loader once, after the first
// overview.
func (m *UI) maybeStartBackgroundLoad() tea.Cmd {
	if m.bgLoad.started {
		return nil
	}
	m.bgLoad.started = true
	gen := m.bgLoad.gen
	return tea.Tick(bgStartDelay, func(time.Time) tea.Msg { return bgLoadStartMsg{gen: gen} })
}

func (m *UI) handleBgLoadStart(msg bgLoadStartMsg) tea.Cmd {
	if msg.gen != m.bgLoad.gen {
		return nil
	}
	m.bgLoad.ctx, m.bgLoad.cancel = context.WithCancel(context.Background())
	m.bgLoad.queue = m.leftSidebar.BackgroundLoadRoots(m.com.Workspace.BaseDir())
	m.bgLoad.lastApply = time.Now()
	startuptrace.Mark("overviews_bg_start")
	return m.fillBackgroundLanes()
}

// fillBackgroundLanes starts paging queued roots until bgConcurrency are
// in flight, or finishes once nothing is queued or in flight.
func (m *UI) fillBackgroundLanes() tea.Cmd {
	var cmds []tea.Cmd
	for m.bgLoad.inflight < bgConcurrency && len(m.bgLoad.queue) > 0 {
		root := m.bgLoad.queue[0]
		m.bgLoad.queue = m.bgLoad.queue[1:]
		cmds = append(cmds, m.fetchBackgroundPage(root, ""))
	}
	if m.bgLoad.inflight == 0 {
		m.finishBackgroundLoad()
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *UI) fetchBackgroundPage(root, cursor string) tea.Cmd {
	m.bgLoad.inflight++
	gen, ctx, ws := m.bgLoad.gen, m.bgLoad.ctx, m.com.Workspace
	return func() tea.Msg {
		ov, err := ws.ListWorkspaceSessionsPage(ctx, root, cursor, bgPageSize)
		return bgPageMsg{gen: gen, root: root, ov: ov, err: err}
	}
}

func (m *UI) handleBgPage(msg bgPageMsg) tea.Cmd {
	if msg.gen != m.bgLoad.gen || m.bgLoad.cancel == nil || m.bgLoad.done {
		return nil
	}
	m.bgLoad.inflight--
	if msg.err != nil {
		if m.com.Workspace.ConnectionState() != workspace.ConnectionStateConnected {
			// Lost the server: start over once overviews flow again.
			m.resetBackgroundLoad()
			return nil
		}
		slog.Debug("Failed to page workspace sessions", "root", msg.root, "error", msg.err)
		return m.fillBackgroundLanes()
	}
	m.leftSidebar.AddBackgroundSessions(msg.root, msg.ov.Sessions)
	m.bgLoad.pending = true
	if m.shouldApplyBackground() {
		m.applyBackgroundSessions()
	}
	if msg.ov.NextCursor != "" {
		return m.fetchBackgroundPage(msg.root, msg.ov.NextCursor)
	}
	return m.fillBackgroundLanes()
}

// shouldApplyBackground reports whether to re-project the sidebar rows now.
// Re-projecting walks (and in the inbox view sorts) every listed session,
// so it is skipped while the navigator is hidden — opening it refreshes the
// overview, which merges everything held — and otherwise spaced so it
// takes at most about a tenth of the time.
func (m *UI) shouldApplyBackground() bool {
	if !m.leftSidebarVisible {
		return false
	}
	return time.Since(m.bgLoad.lastApply) >= max(bgApplyInterval, 10*m.bgLoad.lastCost)
}

func (m *UI) applyBackgroundSessions() {
	if !m.bgLoad.pending {
		return
	}
	start := time.Now()
	m.leftSidebar.ApplyBackgroundSessions()
	m.bgLoad.lastCost = time.Since(start)
	m.bgLoad.maxApply = max(m.bgLoad.maxApply, m.bgLoad.lastCost)
	m.bgLoad.lastApply = time.Now()
	m.bgLoad.pending = false
}

func (m *UI) finishBackgroundLoad() {
	if m.bgLoad.done {
		return
	}
	if m.leftSidebarVisible {
		m.applyBackgroundSessions()
	}
	m.bgLoad.done = true
	if m.bgLoad.cancel != nil {
		m.bgLoad.cancel()
	}
	startuptrace.Count("ui.bg_max_apply_us", int(m.bgLoad.maxApply.Microseconds()))
	startuptrace.Mark("overviews_fully_loaded")
}
