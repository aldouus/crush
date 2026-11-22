package model

import "github.com/taigrr/crush/internal/proto"

// backgroundSessions accumulates sessions the background loader pages in
// beyond a workspace's bounded overview (see [overviewSessionLimit]),
// keyed by overview root. Every overview refresh is bounded again, so the
// accumulated tail is re-merged each time: the refresh's own entries are
// fresher and win, and the tail fills in the rest up to SessionCount.
type backgroundSessions struct {
	byRoot map[string]*bgWorkspace
}

type bgWorkspace struct {
	// sessions is the paged-in tail in server display order, deduped.
	sessions []proto.SessionOverview
	ids      map[string]struct{}
	// fresh is the set of IDs in the sidebar's current overview of this
	// workspace, so later pages skip sessions it already shows.
	fresh map[string]struct{}
	// merged is how many of sessions were already merged into it.
	merged int
}

// add records a page of root's sessions, ignoring ones already held.
func (b *backgroundSessions) add(root string, page []proto.SessionOverview) {
	if b.byRoot == nil {
		b.byRoot = map[string]*bgWorkspace{}
	}
	w := b.byRoot[root]
	if w == nil {
		w = &bgWorkspace{ids: map[string]struct{}{}}
		b.byRoot[root] = w
	}
	for _, sess := range page {
		if _, ok := w.ids[sess.ID]; ok {
			continue
		}
		w.ids[sess.ID] = struct{}{}
		w.sessions = append(w.sessions, sess)
	}
}

// edit applies an [SessionsSidebar.EditSessions] callback to the held
// sessions so removals and local edits survive the next re-merge.
func (b *backgroundSessions) edit(fn func(*proto.SessionOverview) bool) {
	for _, w := range b.byRoot {
		kept := w.sessions[:0]
		for _, sess := range w.sessions {
			if fn(&sess) {
				kept = append(kept, sess)
			} else {
				delete(w.ids, sess.ID)
			}
		}
		clear(w.sessions[len(kept):])
		w.sessions = kept
		w.merged = 0
	}
}

// reset drops everything held.
func (b *backgroundSessions) reset() { b.byRoot = nil }

// mergeInto appends held sessions to each bounded overview they belong to.
// A complete overview (SessionCount zero) supersedes what is held.
// Overviews may already hold merged sessions (EditSessions re-sets the
// merged list); those count as fresh, so nothing is duplicated.
func (b *backgroundSessions) mergeInto(ovs []proto.WorkspaceOverview) {
	if len(b.byRoot) == 0 {
		return
	}
	for i := range ovs {
		w := b.byRoot[ovs[i].Root]
		if w == nil {
			continue
		}
		if ovs[i].SessionCount == 0 {
			delete(b.byRoot, ovs[i].Root)
			continue
		}
		w.fresh = make(map[string]struct{}, len(ovs[i].Sessions))
		for _, sess := range ovs[i].Sessions {
			w.fresh[sess.ID] = struct{}{}
		}
		w.merged = 0
		w.appendNew(&ovs[i])
	}
}

// appendNew merges held sessions not yet merged into ov, up to its count.
// It reports whether anything was appended.
func (w *bgWorkspace) appendNew(ov *proto.WorkspaceOverview) bool {
	appended := false
	for _, sess := range w.sessions[w.merged:] {
		if len(ov.Sessions) >= ov.SessionCount {
			break
		}
		if _, ok := w.fresh[sess.ID]; ok {
			continue
		}
		ov.Sessions = append(ov.Sessions, sess)
		appended = true
	}
	w.merged = len(w.sessions)
	return appended
}

// AddBackgroundSessions records a page of root's sessions from the
// background loader without touching the rows; [ApplyBackgroundSessions]
// shows them.
func (s *SessionsSidebar) AddBackgroundSessions(root string, page []proto.SessionOverview) {
	s.bg.add(root, page)
}

// ApplyBackgroundSessions merges sessions recorded since the last apply
// into the listed overviews, re-sorting only workspaces that grew. Rows
// already shown keep their relative order.
func (s *SessionsSidebar) ApplyBackgroundSessions() {
	var changed []bool
	for wi := range s.overviews {
		ov := &s.overviews[wi]
		w := s.bg.byRoot[ov.Root]
		if w == nil || ov.SessionCount <= len(ov.Sessions) {
			continue
		}
		if w.fresh == nil {
			w.fresh = make(map[string]struct{}, len(ov.Sessions))
			for _, sess := range ov.Sessions {
				w.fresh[sess.ID] = struct{}{}
			}
		}
		if w.appendNew(ov) {
			s.sortWorkspaceSessions(wi)
			if changed == nil {
				changed = make([]bool, len(s.overviews))
			}
			changed[wi] = true
		}
	}
	if changed == nil {
		return
	}
	prevID := s.selectedSessionID()
	if s.mode == sidebarModeInbox && s.proj.inboxValid {
		countsValid := s.proj.countsValid && len(s.proj.counts) == len(s.overviews)
		s.reprojectInbox(changed)
		if countsValid {
			q := s.searchQuery()
			for wi, c := range changed {
				if c {
					s.proj.counts[wi] = s.workspaceCounts(q, wi)
				}
			}
		}
	} else {
		counts := s.proj.counts
		countsValid := s.proj.countsValid && len(counts) == len(s.overviews)
		s.rebuildRows()
		if countsValid {
			q := s.searchQuery()
			for wi, c := range changed {
				if c {
					counts[wi] = s.workspaceCounts(q, wi)
				}
			}
			s.proj.counts = counts
			s.proj.countsValid = true
		}
	}
	s.restoreCursor(prevID)
}

// ClearBackgroundSessions drops everything the background loader added
// (used when roots change meaning, e.g. after a workspace switch).
func (s *SessionsSidebar) ClearBackgroundSessions() { s.bg.reset() }

// BackgroundLoadRoots lists the local workspaces whose overview is bounded,
// current first, then in display order.
func (s *SessionsSidebar) BackgroundLoadRoots(current string) []string {
	var roots []string
	for _, ov := range s.overviews {
		if ov.NodeID != "" || ov.Unavailable || ov.SessionCount <= len(ov.Sessions) {
			continue
		}
		if ov.Root == current {
			roots = append([]string{ov.Root}, roots...)
			continue
		}
		roots = append(roots, ov.Root)
	}
	return roots
}
