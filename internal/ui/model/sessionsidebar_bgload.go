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
// input: it starts a moment after the first overview lands, keeps one
// request in flight, pauses between pages, and re-projects the sidebar
// rows at most every bgApplyInterval.
const (
	bgStartDelay    = 250 * time.Millisecond
	bgPageDelay     = 10 * time.Millisecond
	bgApplyInterval = 300 * time.Millisecond
	bgPageSize      = 500
)

// backgroundLoader is the state of the progressive session loader. gen
// invalidates in-flight pages after a reset.
type backgroundLoader struct {
	gen       uint64
	cancel    context.CancelFunc
	started   bool
	done      bool
	queue     []string
	cursor    string
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
	ctx, cancel := context.WithCancel(context.Background())
	m.bgLoad.cancel = cancel
	m.bgLoad.queue = m.leftSidebar.BackgroundLoadRoots(m.com.Workspace.BaseDir())
	m.bgLoad.lastApply = time.Now()
	startuptrace.Mark("overviews_bg_start")
	return m.nextBackgroundPage(ctx, 0)
}

// nextBackgroundPage fetches the next page after delay, or finishes.
func (m *UI) nextBackgroundPage(ctx context.Context, delay time.Duration) tea.Cmd {
	if len(m.bgLoad.queue) == 0 {
		m.finishBackgroundLoad()
		return nil
	}
	gen, root, cursor := m.bgLoad.gen, m.bgLoad.queue[0], m.bgLoad.cursor
	ws := m.com.Workspace
	fetch := func() tea.Msg {
		ov, err := ws.ListWorkspaceSessionsPage(ctx, root, cursor, bgPageSize)
		return bgPageMsg{gen: gen, root: root, ov: ov, err: err}
	}
	if delay <= 0 {
		return fetch
	}
	return tea.Tick(delay, func(time.Time) tea.Msg { return fetch() })
}

func (m *UI) handleBgPage(msg bgPageMsg) tea.Cmd {
	if msg.gen != m.bgLoad.gen || m.bgLoad.cancel == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.bgLoad.cancel()
	m.bgLoad.cancel = cancel
	if msg.err != nil {
		if m.com.Workspace.ConnectionState() != workspace.ConnectionStateConnected {
			// Lost the server: start over once overviews flow again.
			m.resetBackgroundLoad()
			return nil
		}
		slog.Debug("Failed to page workspace sessions", "root", msg.root, "error", msg.err)
		m.popBackgroundRoot()
		return m.nextBackgroundPage(ctx, bgPageDelay)
	}
	m.leftSidebar.AddBackgroundSessions(msg.root, msg.ov.Sessions)
	m.bgLoad.pending = true
	if msg.ov.NextCursor == "" {
		m.popBackgroundRoot()
	} else {
		m.bgLoad.cursor = msg.ov.NextCursor
	}
	if m.shouldApplyBackground() {
		m.applyBackgroundSessions()
	}
	return m.nextBackgroundPage(ctx, bgPageDelay)
}

func (m *UI) popBackgroundRoot() {
	m.bgLoad.queue = m.bgLoad.queue[1:]
	m.bgLoad.cursor = ""
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
