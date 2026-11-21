package model

import (
	"context"
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles/themes"
	"github.com/taigrr/crush/internal/workspace"
)

// bgSessions builds n sessions of root in server display order.
func bgSessions(prefix string, n int) []proto.SessionOverview {
	out := make([]proto.SessionOverview, n)
	for i := range out {
		out[i] = proto.SessionOverview{ID: fmt.Sprintf("%s%03d", prefix, i), UpdatedAt: int64(10_000 - i)}
	}
	return out
}

func boundedOverview(root string, all []proto.SessionOverview, limit int) proto.WorkspaceOverview {
	return proto.WorkspaceOverview{Root: root, Sessions: append([]proto.SessionOverview(nil), all[:limit]...), SessionCount: len(all)}
}

func sessionIDs(ov proto.WorkspaceOverview) []string {
	ids := make([]string, len(ov.Sessions))
	for i, s := range ov.Sessions {
		ids[i] = s.ID
	}
	return ids
}

// TestBackgroundSessions_MergeDedupeOrder verifies paged sessions are merged
// once, in order, after the bounded rows, and survive bounded refreshes.
func TestBackgroundSessions_MergeDedupeOrder(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	all := bgSessions("a", 30)
	s.SetOverviews([]proto.WorkspaceOverview{boundedOverview("/a", all, 5)})
	shown := sessionIDs(s.overviews[0])

	// Overlapping pages: the first repeats bounded rows, the second
	// repeats part of the first.
	s.AddBackgroundSessions("/a", all[3:15])
	s.AddBackgroundSessions("/a", all[10:30])
	s.ApplyBackgroundSessions()
	require.Equal(t, sessionIDs(proto.WorkspaceOverview{Sessions: all}), sessionIDs(s.overviews[0]))
	require.Equal(t, shown, sessionIDs(s.overviews[0])[:5], "shown rows keep their order")
	require.Zero(t, s.hiddenSessions(0))

	// Applying again is a no-op; a bounded refresh re-merges.
	s.ApplyBackgroundSessions()
	require.Len(t, s.overviews[0].Sessions, 30)
	s.SetOverviews([]proto.WorkspaceOverview{boundedOverview("/a", all, 5)})
	require.Len(t, s.overviews[0].Sessions, 30)

	// A complete listing supersedes held sessions.
	s.SetOverviews([]proto.WorkspaceOverview{{Root: "/a", Sessions: all[:2]}})
	require.Len(t, s.overviews[0].Sessions, 2)
	s.SetOverviews([]proto.WorkspaceOverview{boundedOverview("/a", all, 5)})
	require.Len(t, s.overviews[0].Sessions, 5)
}

// TestBackgroundSessions_LiveUpdateWins verifies a session refreshed in a
// bounded overview keeps its fresh data over an older paged copy.
func TestBackgroundSessions_LiveUpdateWins(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	all := bgSessions("a", 20)
	s.SetOverviews([]proto.WorkspaceOverview{boundedOverview("/a", all, 5)})
	s.AddBackgroundSessions("/a", all[5:20])

	// a010 got updated meanwhile: it is now first, retitled and busy.
	fresh := boundedOverview("/a", all, 5)
	upd := all[10]
	upd.Title, upd.IsBusy, upd.UpdatedAt = "live", true, 99_999
	fresh.Sessions = append([]proto.SessionOverview{upd}, fresh.Sessions[:4]...)
	s.SetOverviews([]proto.WorkspaceOverview{fresh})
	s.ApplyBackgroundSessions()

	ov := s.overviews[0]
	// a004 fell out of the bounded rows but was never paged (it was on the
	// first page), so it is missing until a complete listing; nothing is
	// duplicated.
	require.Len(t, ov.Sessions, 19)
	n := 0
	for _, sess := range ov.Sessions {
		if sess.ID == "a010" {
			n++
			require.Equal(t, "live", sess.Title)
		}
	}
	require.Equal(t, 1, n)
	require.Equal(t, "a010", ov.Sessions[0].ID)
}

// TestBackgroundSessions_DeleteDuringLoad verifies a session removed while
// paging is not resurrected by held pages or later merges.
func TestBackgroundSessions_DeleteDuringLoad(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	all := bgSessions("a", 20)
	s.SetOverviews([]proto.WorkspaceOverview{boundedOverview("/a", all, 5)})
	s.AddBackgroundSessions("/a", all[5:12])
	s.ApplyBackgroundSessions()
	s.EditSessions(func(o *proto.SessionOverview) bool { return o.ID != "a007" })
	s.AddBackgroundSessions("/a", all[12:20])
	s.ApplyBackgroundSessions()
	s.SetOverviews([]proto.WorkspaceOverview{boundedOverview("/a", all, 5)})
	require.NotContains(t, sessionIDs(s.overviews[0]), "a007")
	require.Len(t, s.overviews[0].Sessions, 19)
}

// pagingStub serves bounded pages of per-root session lists.
type pagingStub struct {
	*archiveStubWorkspace
	sessions map[string][]proto.SessionOverview
	calls    []string
	fail     map[string]bool
	state    workspace.ConnectionState
	limits   []int
}

func (p *pagingStub) ListWorkspaceOverviewsLimit(_ context.Context, limit int) ([]proto.WorkspaceOverview, error) {
	p.limits = append(p.limits, limit)
	return nil, nil
}

func (p *pagingStub) BaseDir() string { return "/cur" }

func (p *pagingStub) ConnectionState() workspace.ConnectionState { return p.state }

func (p *pagingStub) ListWorkspaceSessionsPage(_ context.Context, root, cursor string, limit int) (proto.WorkspaceOverview, error) {
	p.calls = append(p.calls, root+"@"+cursor)
	if p.fail[root] {
		return proto.WorkspaceOverview{}, errors.New("boom")
	}
	all := p.sessions[root]
	start := 0
	if cursor != "" {
		fmt.Sscanf(cursor, "%d", &start)
	}
	end := min(len(all), start+limit)
	ov := proto.WorkspaceOverview{Root: root, Sessions: all[start:end], SessionCount: len(all)}
	if end < len(all) {
		ov.NextCursor = fmt.Sprint(end)
	}
	return ov, nil
}

func newBgTestUI(t *testing.T, ws *pagingStub) *UI {
	t.Helper()
	sty := themes.CharmtonePantera()
	com := &common.Common{Styles: &sty, Workspace: ws}
	s := NewSessionsSidebar(com)
	s.mode = sidebarModeSessions
	return &UI{com: com, leftSidebar: s}
}

// drive runs the loader to completion, feeding each command's messages
// back in FIFO order (batched lanes are queued individually).
func drive(m *UI, cmd func() any) {
	queue := []func() any{cmd}
	for i := 0; i < 1000 && len(queue) > 0; i++ {
		var next tea.Cmd
		switch msg := queue[0]().(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				if c != nil {
					queue = append(queue, func() any { return c() })
				}
			}
		case bgLoadStartMsg:
			next = m.handleBgLoadStart(msg)
		case bgPageMsg:
			next = m.handleBgPage(msg)
		}
		queue = queue[1:]
		if next != nil {
			queue = append(queue, func() any { return next() })
		}
	}
}

// TestBackgroundLoader_LoadsAllCurrentFirst verifies the loader pages every
// bounded local workspace, the current one first, and marks completion.
func TestBackgroundLoader_LoadsAllCurrentFirst(t *testing.T) {
	t.Parallel()
	ws := &pagingStub{archiveStubWorkspace: &archiveStubWorkspace{}, state: workspace.ConnectionStateConnected, sessions: map[string][]proto.SessionOverview{
		"/x":   bgSessions("x", 2*bgPageSize+200),
		"/cur": bgSessions("c", 700),
		"/bad": bgSessions("b", 100),
	}}
	ws.fail = map[string]bool{"/bad": true}
	m := newBgTestUI(t, ws)
	m.leftSidebarVisible = true
	remote := boundedOverview("/r", bgSessions("r", 100), 5)
	remote.NodeID = "other"
	m.leftSidebar.SetOverviews([]proto.WorkspaceOverview{
		boundedOverview("/x", ws.sessions["/x"], 50),
		boundedOverview("/bad", ws.sessions["/bad"], 50),
		boundedOverview("/cur", ws.sessions["/cur"], 50),
		{Root: "/small", Sessions: bgSessions("s", 3)},
		remote,
	})
	require.NotNil(t, m.maybeStartBackgroundLoad())
	require.Nil(t, m.maybeStartBackgroundLoad(), "starts once")
	drive(m, func() any { return bgLoadStartMsg{gen: m.bgLoad.gen} })

	p1, p2 := fmt.Sprint(bgPageSize), fmt.Sprint(2*bgPageSize)
	require.Equal(t, []string{"/cur@", "/x@", "/bad@", "/x@" + p1, "/x@" + p2}, ws.calls)
	require.True(t, m.bgLoad.complete())
	for _, ov := range m.leftSidebar.overviews {
		switch ov.Root {
		case "/x", "/cur":
			require.Len(t, ov.Sessions, len(ws.sessions[ov.Root]), ov.Root)
		case "/bad":
			require.Len(t, ov.Sessions, 50)
		case "/r":
			require.Len(t, ov.Sessions, 5)
		}
	}
}

// TestBackgroundLoader_SearchFullFetchUntilLoaded verifies "/" search asks
// for unbounded overviews while the background load is incomplete, and
// once it completes searches the paged-in sessions with bounded refreshes.
func TestBackgroundLoader_SearchFullFetchUntilLoaded(t *testing.T) {
	t.Parallel()
	all := bgSessions("c", 700)
	all[650].Title = "needle in the tail"
	ws := &pagingStub{archiveStubWorkspace: &archiveStubWorkspace{}, state: workspace.ConnectionStateConnected, sessions: map[string][]proto.SessionOverview{
		"/cur": all,
	}}
	m := newBgTestUI(t, ws)
	m.leftSidebarVisible = true
	m.leftSidebar.SetOverviews([]proto.WorkspaceOverview{boundedOverview("/cur", all, 50)})
	m.maybeStartBackgroundLoad()

	m.leftSidebar.EnterSearch()
	require.True(t, m.leftSidebar.Truncated())
	m.loadWorkspaceOverviews()()
	m.leftSidebar.ExitSearch()

	drive(m, func() any { return bgLoadStartMsg{gen: m.bgLoad.gen} })
	require.True(t, m.bgLoad.complete())
	require.False(t, m.leftSidebar.Truncated())

	m.leftSidebar.EnterSearch()
	m.loadWorkspaceOverviews()()
	typeQuery(m.leftSidebar, "needle")
	require.Equal(t, []string{"c650"}, sessionOrder(m.leftSidebar))
	require.Equal(t, []int{0, overviewSessionLimit}, ws.limits)
}

// TestBackgroundLoader_HiddenSidebarDefersRows verifies a hidden navigator
// only records pages; the next overview (opening it refreshes) shows them.
func TestBackgroundLoader_HiddenSidebarDefersRows(t *testing.T) {
	t.Parallel()
	ws := &pagingStub{archiveStubWorkspace: &archiveStubWorkspace{}, state: workspace.ConnectionStateConnected, sessions: map[string][]proto.SessionOverview{
		"/cur": bgSessions("c", 700),
	}}
	m := newBgTestUI(t, ws)
	bounded := boundedOverview("/cur", ws.sessions["/cur"], 50)
	m.leftSidebar.SetOverviews([]proto.WorkspaceOverview{bounded})
	m.maybeStartBackgroundLoad()
	drive(m, func() any { return bgLoadStartMsg{gen: m.bgLoad.gen} })
	require.True(t, m.bgLoad.complete())
	require.Len(t, m.leftSidebar.overviews[0].Sessions, 50)
	m.leftSidebar.SetOverviews([]proto.WorkspaceOverview{boundedOverview("/cur", ws.sessions["/cur"], 50)})
	require.Len(t, m.leftSidebar.overviews[0].Sessions, 700)
}

// TestBackgroundLoader_CancelledByReset verifies pages from before a reset
// (workspace switch) are dropped and the context is cancelled.
func TestBackgroundLoader_CancelledByReset(t *testing.T) {
	t.Parallel()
	ws := &pagingStub{archiveStubWorkspace: &archiveStubWorkspace{}, state: workspace.ConnectionStateConnected, sessions: map[string][]proto.SessionOverview{
		"/cur": bgSessions("c", 700),
	}}
	m := newBgTestUI(t, ws)
	m.leftSidebar.SetOverviews([]proto.WorkspaceOverview{boundedOverview("/cur", ws.sessions["/cur"], 50)})
	m.maybeStartBackgroundLoad()
	cmd := m.handleBgLoadStart(bgLoadStartMsg{gen: m.bgLoad.gen})
	require.NotNil(t, cmd)
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		msg = batch[0]()
	}
	page := msg.(bgPageMsg)
	ctx := m.bgLoad.ctx
	m.resetBackgroundLoad()
	require.Error(t, ctx.Err())
	require.Nil(t, m.handleBgPage(page))
	require.Len(t, m.leftSidebar.overviews[0].Sessions, 50)
	require.False(t, m.bgLoad.started)
}

// TestBackgroundLoader_ResetsOnDisconnect verifies a failed page while the
// connection is down restarts loading later instead of skipping roots.
func TestBackgroundLoader_ResetsOnDisconnect(t *testing.T) {
	t.Parallel()
	ws := &pagingStub{archiveStubWorkspace: &archiveStubWorkspace{}, state: workspace.ConnectionStateReconnecting, sessions: map[string][]proto.SessionOverview{
		"/cur": bgSessions("c", 700),
	}}
	ws.fail = map[string]bool{"/cur": true}
	m := newBgTestUI(t, ws)
	m.leftSidebar.SetOverviews([]proto.WorkspaceOverview{boundedOverview("/cur", ws.sessions["/cur"], 50)})
	m.maybeStartBackgroundLoad()
	drive(m, func() any { return bgLoadStartMsg{gen: m.bgLoad.gen} })
	require.False(t, m.bgLoad.started)
	require.False(t, m.bgLoad.complete())
}
