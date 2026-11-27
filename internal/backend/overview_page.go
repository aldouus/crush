package backend

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/taigrr/crush/internal/proto"
)

// ListWorkspaceSessionsPage returns one page of a single local workspace's
// sessions in overview display order: the sessions sorting strictly after
// cursor (empty = from the start), at most limit of them (<= 0 = all).
// SessionCount carries the workspace's total and NextCursor is set when more
// sessions follow. The cursor encodes the last session's ordering keys and
// ID, so sessions inserted or removed between pages shift neither earlier
// nor later pages; only a session whose keys changed can move across the
// cursor, and live updates cover those.
//
// Detached workspaces page out of the peek cache, so each page costs a stat
// of the database files rather than a re-read.
func (b *Backend) ListWorkspaceSessionsPage(ctx context.Context, root, cursor string, limit int) (proto.WorkspaceOverview, error) {
	var after *overviewCursor
	if cursor != "" {
		c, err := parseOverviewCursor(cursor)
		if err != nil {
			return proto.WorkspaceOverview{}, err
		}
		after = &c
	}
	ov, all, err := b.workspaceSessions(ctx, root)
	if err != nil {
		return proto.WorkspaceOverview{}, err
	}
	owned := make([]proto.SessionOverview, 0, len(all))
	for _, s := range all {
		if b.ownedHere(s.OwnerNode) {
			s.OwnerNode = ""
			owned = append(owned, s)
		}
	}
	start := 0
	if after != nil {
		start = sort.Search(len(owned), func(i int) bool { return after.before(owned[i]) })
	}
	end := len(owned)
	if limit > 0 {
		end = min(end, start+limit)
	}
	ov.Sessions = slices.Clip(owned[start:end])
	ov.SessionCount = len(owned)
	if end < len(owned) && end > start {
		ov.NextCursor = cursorFor(owned[end-1]).String()
	}
	return ov, nil
}

// workspaceSessions lists one local workspace's sessions in display order,
// with its overview header fields filled in.
func (b *Backend) workspaceSessions(ctx context.Context, root string) (proto.WorkspaceOverview, []proto.SessionOverview, error) {
	for _, ws := range b.workspaces.Seq2() {
		if ws.resolvedPath != root {
			continue
		}
		ov := proto.WorkspaceOverview{Root: root, WorkspaceID: ws.ID, Attached: true}
		if ws.Cfg != nil {
			ov.DataDir = ws.Cfg.Config().Options.DataDirectory
		}
		sessions := b.attachedSessionOverviews(ctx, ws)
		sortSessionOverviews(sessions)
		return ov, sessions, nil
	}
	if b.registry == nil {
		return proto.WorkspaceOverview{}, nil, ErrUnknownRoot
	}
	entries, err := b.registry.List()
	if err != nil {
		return proto.WorkspaceOverview{}, nil, err
	}
	for _, e := range entries {
		if e.Root != root {
			continue
		}
		ov := proto.WorkspaceOverview{Root: e.Root, DataDir: e.DataDir}
		sessions, staleSince, err := b.peekCache.overviews(ctx, e.DataDir)
		switch {
		case err == nil:
		case !staleSince.IsZero():
			ov.Stale, ov.StaleSince, ov.LastError = true, staleSince.UnixMilli(), err.Error()
		default:
			return proto.WorkspaceOverview{}, nil, err
		}
		return ov, sessions, nil
	}
	return proto.WorkspaceOverview{}, nil, ErrUnknownRoot
}

// overviewCursor is the ordering key of the last session on a page.
type overviewCursor struct {
	busy, unread bool
	updatedAt    int64
	id           string
}

func cursorFor(s proto.SessionOverview) overviewCursor {
	return overviewCursor{busy: s.IsBusy, unread: s.Unread, updatedAt: s.UpdatedAt, id: s.ID}
}

func (c overviewCursor) String() string {
	return fmt.Sprintf("%s.%s.%d.%s", boolDigit(c.busy), boolDigit(c.unread), c.updatedAt, c.id)
}

func boolDigit(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func parseOverviewCursor(s string) (overviewCursor, error) {
	parts := strings.SplitN(s, ".", 4)
	if len(parts) != 4 || (parts[0] != "0" && parts[0] != "1") || (parts[1] != "0" && parts[1] != "1") {
		return overviewCursor{}, ErrInvalidCursor
	}
	updated, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return overviewCursor{}, ErrInvalidCursor
	}
	return overviewCursor{busy: parts[0] == "1", unread: parts[1] == "1", updatedAt: updated, id: parts[3]}, nil
}

// before reports whether the cursor sorts strictly before s in
// [sortSessionOverviews] order.
func (c overviewCursor) before(s proto.SessionOverview) bool {
	return sessionLess(c.busy, c.unread, c.updatedAt, c.id, s.IsBusy, s.Unread, s.UpdatedAt, s.ID)
}

func sessionLess(aBusy, aUnread bool, aUpdated int64, aID string, bBusy, bUnread bool, bUpdated int64, bID string) bool {
	if aBusy != bBusy {
		return aBusy
	}
	if aUnread != bUnread {
		return aUnread
	}
	if aUpdated != bUpdated {
		return aUpdated > bUpdated
	}
	return aID < bID
}
