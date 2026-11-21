package model

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/taigrr/crush/internal/proto"
)

// jankSidebar builds a sidebar with ws bounded workspaces of n sessions
// each (50 shown) plus the full per-root session lists for paging.
func jankSidebar(tb testing.TB, ws, n int, inbox bool) (*SessionsSidebar, [][]proto.SessionOverview) {
	tb.Helper()
	s := newTestSidebar(tb)
	if inbox {
		s.mode = sidebarModeInbox
	}
	all := make([][]proto.SessionOverview, ws)
	ovs := make([]proto.WorkspaceOverview, ws)
	for w := range ws {
		root := fmt.Sprintf("/w%03d", w)
		sess := make([]proto.SessionOverview, n)
		for i := range sess {
			sess[i] = proto.SessionOverview{
				ID:        fmt.Sprintf("%s-%06d", root, i),
				Title:     fmt.Sprintf("session %d of %s", i, root),
				UpdatedAt: int64(10_000_000 - i*ws - w),
				Unread:    i%97 == 0,
			}
		}
		slices.SortStableFunc(sess, func(a, b proto.SessionOverview) int {
			if a.Unread != b.Unread {
				if a.Unread {
					return -1
				}
				return 1
			}
			return int(b.UpdatedAt - a.UpdatedAt)
		})
		all[w] = sess
		ovs[w] = boundedOverview(root, sess, min(50, n))
	}
	s.SetOverviews(ovs)
	_ = s.Render(60, 50, true)
	return s, all
}

func loadAll(s *SessionsSidebar, all [][]proto.SessionOverview) {
	for w, sess := range all {
		s.AddBackgroundSessions(fmt.Sprintf("/w%03d", w), sess[50:])
	}
	s.ApplyBackgroundSessions()
}

func pct(d []time.Duration, p float64) time.Duration {
	slices.Sort(d)
	return d[min(len(d)-1, int(float64(len(d))*p))]
}

// TestSidebarJankProbe replays a full background load with the navigator
// visible, one apply + frame per page, and reports the event-loop gaps.
// By default it runs a small 10x200 replay that only checks every session
// ends up listed; set CRUSH_JANK_PROBE=1 SIDEBAR_JANK=WSxN (e.g. 100x5000)
// for the heavy probe
// and SIDEBAR_JANK_MODE=inbox|grouped to replay only one view.
func TestSidebarJankProbe(t *testing.T) {
	t.Parallel()
	spec := ""
	if os.Getenv("CRUSH_JANK_PROBE") == "1" {
		spec = os.Getenv("SIDEBAR_JANK")
	}
	if spec == "" {
		spec = "10x200"
	}
	var ws, n int
	_, err := fmt.Sscanf(spec, "%dx%d", &ws, &n)
	if err != nil {
		t.Fatal(err)
	}
	page := bgPageSize
	if v, _ := strconv.Atoi(os.Getenv("SIDEBAR_JANK_PAGE")); v > 0 {
		page = v
	}
	modes := []bool{true, false}
	switch os.Getenv("SIDEBAR_JANK_MODE") {
	case "inbox":
		modes = []bool{true}
	case "grouped":
		modes = []bool{false}
	}
	for _, inbox := range modes {
		s, all := jankSidebar(t, ws, n, inbox)
		var gaps, frames, keys []time.Duration
		for w, sess := range all {
			root := fmt.Sprintf("/w%03d", w)
			for i := 50; i < len(sess); i += page {
				start := time.Now()
				s.AddBackgroundSessions(root, sess[i:min(len(sess), i+page)])
				s.ApplyBackgroundSessions()
				f := time.Now()
				_ = s.Render(60, 50, true)
				frames = append(frames, time.Since(f))
				gaps = append(gaps, time.Since(start))
			}
			k := time.Now()
			s.MoveDown()
			_ = s.Render(60, 50, true)
			keys = append(keys, time.Since(k))
		}
		for w := range all {
			if got := len(s.overviews[w].Sessions); got != n {
				t.Fatalf("inbox=%v workspace %d: %d sessions listed, want %d", inbox, w, got, n)
			}
		}
		t.Logf("%s inbox=%v pages=%d gap max=%v p99=%v p50=%v frame max=%v p99=%v key max=%v p99=%v",
			spec, inbox, len(gaps), slices.Max(gaps), pct(gaps, 0.99), pct(gaps, 0.5),
			slices.Max(frames), pct(frames, 0.99), slices.Max(keys), pct(keys, 0.99))
	}
}

// benchSizes defaults to a quick matrix; SIDEBAR_JANK=WSxN benchmarks
// just that size.
func benchSizes() []struct{ ws, n int } {
	var ws, n int
	if _, err := fmt.Sscanf(os.Getenv("SIDEBAR_JANK"), "%dx%d", &ws, &n); err == nil {
		return []struct{ ws, n int }{{ws, n}}
	}
	return []struct{ ws, n int }{{10, 200}, {100, 2000}, {100, 5000}}
}

// BenchmarkSidebarFrame measures one frame of a fully loaded navigator.
func BenchmarkSidebarFrame(b *testing.B) {
	for _, sz := range benchSizes() {
		for _, inbox := range []bool{true, false} {
			b.Run(fmt.Sprintf("%dx%d/inbox=%v", sz.ws, sz.n, inbox), func(b *testing.B) {
				s, all := jankSidebar(b, sz.ws, sz.n, inbox)
				loadAll(s, all)
				b.ResetTimer()
				for range b.N {
					_ = s.Render(60, 50, true)
				}
			})
		}
	}
}

// BenchmarkSidebarApplyPage measures merging one page into a navigator
// that already holds all but that page, plus the next frame.
func BenchmarkSidebarApplyPage(b *testing.B) {
	for _, sz := range benchSizes() {
		for _, inbox := range []bool{true, false} {
			b.Run(fmt.Sprintf("%dx%d/inbox=%v", sz.ws, sz.n, inbox), func(b *testing.B) {
				s, all := jankSidebar(b, sz.ws, sz.n, inbox)
				last := len(all) - 1
				for w, sess := range all {
					if w != last {
						s.AddBackgroundSessions(fmt.Sprintf("/w%03d", w), sess[50:])
					}
				}
				s.ApplyBackgroundSessions()
				root := fmt.Sprintf("/w%03d", last)
				tail := all[last][50:]
				p := min(bgPageSize, len(tail))
				for range b.N {
					b.StopTimer()
					delete(s.bg.byRoot, root)
					ovs := slices.Clone(s.overviews)
					ovs[last] = boundedOverview(root, all[last], 50)
					s.overviews = ovs
					b.StartTimer()
					s.AddBackgroundSessions(root, tail[:p])
					s.ApplyBackgroundSessions()
					_ = s.Render(60, 50, true)
				}
			})
		}
	}
}

// BenchmarkSidebarScale measures one frame and one navigate-down keypress
// (move + frame) of a fully loaded navigator at 20k and 100k sessions.
func BenchmarkSidebarScale(b *testing.B) {
	for _, sz := range []struct{ ws, n int }{{20, 1000}, {100, 1000}} {
		for _, inbox := range []bool{true, false} {
			s, all := jankSidebar(b, sz.ws, sz.n, inbox)
			loadAll(s, all)
			name := fmt.Sprintf("%dk/inbox=%v", sz.ws*sz.n/1000, inbox)
			b.Run("frame/"+name, func(b *testing.B) {
				for range b.N {
					_ = s.Render(60, 50, true)
				}
			})
			b.Run("key/"+name, func(b *testing.B) {
				for range b.N {
					s.MoveDown()
					_ = s.Render(60, 50, true)
				}
			})
		}
	}
}
