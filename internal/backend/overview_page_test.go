package backend

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/registry"
)

type fakePeek struct {
	mu       sync.Mutex
	sessions []db.PeekedSession
}

func (f *fakePeek) peek(context.Context, string) ([]db.PeekedSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]db.PeekedSession(nil), f.sessions...), nil
}

func pagingBackend(t *testing.T, n int) (*Backend, *fakePeek) {
	t.Helper()
	b, _ := newTestBackend(t)
	b.registry = registry.NewWithPath(filepath.Join(t.TempDir(), "workspaces.jsonl"))
	require.NoError(t, b.registry.Add(registry.Entry{Root: "/proj/p", DataDir: t.TempDir()}))
	f := &fakePeek{}
	for i := range n {
		// Pairs share UpdatedAt so the ID tiebreak is exercised.
		f.sessions = append(f.sessions, db.PeekedSession{ID: fmt.Sprintf("s%03d", i), UpdatedAt: int64(1000 - i/2)})
	}
	b.peekCache.peek = f.peek
	return b, f
}

func pageAll(t *testing.T, b *Backend, limit int, between func()) []string {
	t.Helper()
	var ids []string
	cursor := ""
	for range 1000 {
		ov, err := b.ListWorkspaceSessionsPage(t.Context(), "/proj/p", cursor, limit)
		require.NoError(t, err)
		require.LessOrEqual(t, len(ov.Sessions), limit)
		for _, s := range ov.Sessions {
			ids = append(ids, s.ID)
		}
		if ov.NextCursor == "" {
			return ids
		}
		cursor = ov.NextCursor
		if between != nil {
			between()
		}
	}
	t.Fatal("paging did not terminate")
	return nil
}

// TestListWorkspaceSessionsPage_MatchesOverview verifies concatenated pages
// equal the full overview list, in order.
func TestListWorkspaceSessionsPage_MatchesOverview(t *testing.T) {
	t.Parallel()
	b, _ := pagingBackend(t, 101)
	full, err := b.ListWorkspaceOverviews(t.Context())
	require.NoError(t, err)
	var want []string
	for _, s := range full[0].Sessions {
		want = append(want, s.ID)
	}
	require.Equal(t, want, pageAll(t, b, 10, nil))

	ov, err := b.ListWorkspaceSessionsPage(t.Context(), "/proj/p", "", 10)
	require.NoError(t, err)
	require.Equal(t, 101, ov.SessionCount)
	require.Equal(t, "/proj/p", ov.Root)
}

// TestListWorkspaceSessionsPage_ConcurrentInserts verifies sessions inserted
// between pages neither duplicate nor drop pre-existing sessions.
func TestListWorkspaceSessionsPage_ConcurrentInserts(t *testing.T) {
	t.Parallel()
	b, f := pagingBackend(t, 50)
	n := 0
	got := pageAll(t, b, 7, func() {
		f.mu.Lock()
		// One newer (lands on an already-served page) and one with a tied
		// key (lands relative to the cursor by ID).
		f.sessions = append(f.sessions,
			db.PeekedSession{ID: fmt.Sprintf("new%02d", n), UpdatedAt: 5000},
			db.PeekedSession{ID: fmt.Sprintf("s%03da", 10+n), UpdatedAt: int64(1000 - (10+n)/2)},
		)
		f.mu.Unlock()
		n++
		b.peekCache.reset()
	})
	seen := map[string]int{}
	for _, id := range got {
		seen[id]++
	}
	for i := range 50 {
		require.Equal(t, 1, seen[fmt.Sprintf("s%03d", i)], "s%03d", i)
	}
	for id, c := range seen {
		require.Equal(t, 1, c, id)
	}
}

func TestListWorkspaceSessionsPage_Errors(t *testing.T) {
	t.Parallel()
	b, _ := pagingBackend(t, 3)
	_, err := b.ListWorkspaceSessionsPage(t.Context(), "/nope", "", 1)
	require.ErrorIs(t, err, ErrUnknownRoot)
	_, err = b.ListWorkspaceSessionsPage(t.Context(), "/proj/p", "garbage", 1)
	require.ErrorIs(t, err, ErrInvalidCursor)
	ov, err := b.ListWorkspaceSessionsPage(t.Context(), "/proj/p", "", 0)
	require.NoError(t, err)
	require.Len(t, ov.Sessions, 3)
	require.Empty(t, ov.NextCursor)
}

// TestListWorkspaceOverviews_LegacyUnchanged verifies listings without
// paging parameters stay complete and carry no cursor.
func TestListWorkspaceOverviews_LegacyUnchanged(t *testing.T) {
	t.Parallel()
	b, _ := pagingBackend(t, 120)
	full, err := b.ListWorkspaceOverviews(t.Context())
	require.NoError(t, err)
	require.Len(t, full[0].Sessions, 120)
	require.Zero(t, full[0].SessionCount)
	require.Empty(t, full[0].NextCursor)
	bounded, err := b.ListWorkspaceOverviewsLimit(t.Context(), 50)
	require.NoError(t, err)
	require.Len(t, bounded[0].Sessions, 50)
	require.Equal(t, 120, bounded[0].SessionCount)
	require.Empty(t, bounded[0].NextCursor)
}
