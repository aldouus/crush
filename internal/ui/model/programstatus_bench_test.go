package model

import (
	"context"
	"fmt"
	"testing"

	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/session"
)

// BenchmarkProgramStatusSteady measures the per-Update cost when nothing
// changed, which is every frame.
func BenchmarkProgramStatusSteady(b *testing.B) {
	u := newTestUI()
	u.com.Workspace = &sendWorkspace{readiness: func(context.Context) (bool, error) { return false, nil }}
	u.session = &session.Session{ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	var ss []proto.SessionOverview
	for i := range 200 {
		ss = append(ss, proto.SessionOverview{ID: fmt.Sprintf("%08d-aaaa-aaaa-aaaa-aaaaaaaaaaaa", i), Title: "t"})
	}
	u.leftSidebar.SetOverviews([]proto.WorkspaceOverview{{Sessions: ss}})
	u.programStatusCmd()
	b.ReportAllocs()
	for b.Loop() {
		u.programStatusCmd()
	}
}
