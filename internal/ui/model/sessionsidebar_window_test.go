package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
)

// assertWindowMatchesFull checks that Render's body equals the slice of a
// full render of every row at the current scroll offset (the pre-windowing
// behaviour).
func assertWindowMatchesFull(t *testing.T, s *SessionsSidebar, width, height int) {
	t.Helper()
	out := strings.Split(s.Render(width, height, true), "\n")
	header := s.fixedHeaderHeight(height)
	body := max(1, height-header)
	full := s.renderRows(width, true, 0, len(s.rows))
	want := full[min(s.scroll, len(full)):min(len(full), s.scroll+body)]
	got := out[header:]
	got = got[:min(len(got), len(want))]
	require.Equal(t, want, got)
	require.GreaterOrEqual(t, s.cursor, s.scroll)
	require.Less(t, s.cursor, s.scroll+body)
}

func TestSidebarWindowedRenderMatchesFull(t *testing.T) {
	t.Parallel()
	for _, inbox := range []bool{true, false} {
		s, all := jankSidebar(t, 4, 60, inbox)
		loadAll(s, all)
		const width, height = 60, 20
		assertWindowMatchesFull(t, s, width, height)
		for range len(s.rows) + 5 {
			s.MoveDown()
			assertWindowMatchesFull(t, s, width, height)
		}
		s.MoveBottom()
		assertWindowMatchesFull(t, s, width, height)
		require.Greater(t, s.scroll, 0)
		for range 30 {
			s.MoveUp()
			assertWindowMatchesFull(t, s, width, height)
		}
		s.MoveTop()
		assertWindowMatchesFull(t, s, width, height)

		s.EditSessions(func(o *proto.SessionOverview) bool {
			if o.ID == "/w002-000055" {
				o.IsBusy = true
				o.UpdatedAt = 1 << 40
				return true
			}
			return false
		})
		assertWindowMatchesFull(t, s, width, height)

		s.EnterSearch()
		s.searchInput.SetValue("session 5")
		s.rebuildRows()
		assertWindowMatchesFull(t, s, width, height)
		s.MoveBottom()
		assertWindowMatchesFull(t, s, width, height)
		s.ExitSearch()
		assertWindowMatchesFull(t, s, width, height)
	}
}
