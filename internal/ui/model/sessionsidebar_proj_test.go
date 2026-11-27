package model

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
)

// TestSidebarIncrementalProjection checks that background applies, which
// patch the cached inbox sections and counts per workspace, always match a
// full rebuild under random pages, edits, deletes, pending and filters.
func TestSidebarIncrementalProjection(t *testing.T) {
	t.Parallel()
	for seed := range uint64(20) {
		for _, inbox := range []bool{true, false} {
			t.Run(fmt.Sprintf("seed=%d/inbox=%v", seed, inbox), func(t *testing.T) {
				t.Parallel()
				rng := rand.New(rand.NewPCG(seed, 7))
				s := newTestSidebar(t)
				if inbox {
					s.mode = sidebarModeInbox
				}
				const ws = 6
				all := make([][]proto.SessionOverview, ws)
				ovs := make([]proto.WorkspaceOverview, ws)
				for w := range ws {
					n := 5 + rng.IntN(60)
					sess := make([]proto.SessionOverview, n)
					for i := range sess {
						sess[i] = proto.SessionOverview{
							ID:        fmt.Sprintf("w%d-%d", w, i),
							Title:     fmt.Sprintf("t%d", rng.IntN(5)),
							UpdatedAt: int64(rng.IntN(20)),
							Unread:    rng.IntN(4) == 0,
							IsBusy:    rng.IntN(8) == 0,
							Favorite:  rng.IntN(10) == 0,
						}
					}
					all[w] = sess
					ovs[w] = boundedOverview(fmt.Sprintf("/w%d", w), sess, min(4, n))
				}
				s.SetOverviews(ovs)
				_ = s.Render(60, 30, true)
				next := make([]int, ws)
				for i := range next {
					next[i] = min(4, len(all[i]))
				}
				for range 60 {
					staleRows := false
					switch rng.IntN(10) {
					case 0:
						pending := map[string]bool{}
						for range 3 {
							w := rng.IntN(ws)
							pending[all[w][rng.IntN(len(all[w]))].ID] = true
						}
						s.SetPendingSessions(pending)
						// Rows are only reprojected on the next rebuild.
						staleRows = true
					case 1:
						drop := all[rng.IntN(ws)][0].ID
						s.EditSessions(func(o *proto.SessionOverview) bool {
							o.Unread = !o.Unread
							return o.ID != drop
						})
					case 2:
						if s.Searching() {
							s.ExitSearch()
						} else {
							s.EnterSearch()
							s.searchInput.SetValue(fmt.Sprintf("t%d", rng.IntN(5)))
							s.rebuildRows()
						}
					default:
						for range 1 + rng.IntN(3) {
							w := rng.IntN(ws)
							end := min(len(all[w]), next[w]+1+rng.IntN(10))
							s.AddBackgroundSessions(fmt.Sprintf("/w%d", w), all[w][next[w]:end])
							next[w] = end
						}
						s.ApplyBackgroundSessions()
					}
					_ = s.Render(60, 30, true)
					gotRows := slices.Clone(s.rows)
					gotCounts := s.SessionCounts()
					s.rebuildRows()
					if !staleRows {
						require.Equal(t, s.rows, gotRows)
					}
					require.Equal(t, s.SessionCounts(), gotCounts)
				}
			})
		}
	}
}
