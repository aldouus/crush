package model

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/question"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/termstatus"
)

func rawOf(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	require.NotNil(t, cmd)
	msg := cmd()
	raw, ok := msg.(tea.RawMsg)
	require.True(t, ok)
	s, ok := raw.Msg.(string)
	require.True(t, ok)
	return s
}

func TestProgramStatusCmd(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.com.Workspace = &sendWorkspace{readiness: func(context.Context) (bool, error) { return false, nil }}
	const openID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const otherID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	u.session = &session.Session{ID: openID, Title: "Open"}
	u.leftSidebar.SetOverviews([]proto.WorkspaceOverview{{
		Sessions: []proto.SessionOverview{
			{ID: openID, Title: "Open"},
			{ID: otherID, Title: "Other", IsBusy: true},
		},
	}})

	first := rawOf(t, u.programStatusCmd())
	require.Contains(t, first, "state=idle:id=")
	require.Contains(t, first, "state=working:id=")
	require.Nil(t, u.programStatusCmd())

	u.pendingPermissions[openID] = &permission.PermissionRequest{SessionID: openID, Description: "Allow bash?"}
	blocked := rawOf(t, u.programStatusCmd())
	require.Contains(t, blocked, "state=blocked:id=")
	require.Contains(t, blocked, "kind=permission")
	require.NotContains(t, blocked, "state=working") // the other session did not change

	delete(u.pendingPermissions, openID)
	u.pendingQuestions[otherID] = &question.Request{SessionID: otherID, Prompt: "Which?"}
	q := rawOf(t, u.programStatusCmd())
	require.Contains(t, q, "kind=question")
	require.Contains(t, q, "state=idle")

	u.leftSidebar.SetOverviews(nil)
	cleared := rawOf(t, u.programStatusCmd())
	require.Contains(t, cleared, "state=clear:id=")
	require.True(t, strings.HasPrefix(cleared, "\x1b]7501;"))
}

func TestNoteRunComplete(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.noteRunComplete(notify.RunComplete{SessionID: "s", Cancelled: true})
	require.Equal(t, termstatus.StateIdle, u.sessionOutcome["s"].state)
	u.noteRunComplete(notify.RunComplete{SessionID: "s", Error: "boom"})
	require.Equal(t, termstatus.StateError, u.sessionOutcome["s"].state)
	u.noteRunComplete(notify.RunComplete{SessionID: "s", Text: "first line\nrest"})
	require.Equal(t, termstatus.StateDone, u.sessionOutcome["s"].state)
	require.Equal(t, "first line", u.sessionOutcome["s"].msg)
}

func TestRecordIDStable(t *testing.T) {
	t.Parallel()
	a := recordID("11111111-1111-1111-1111-111111111111")
	b := recordID("22222222-2222-2222-2222-111111111111")
	require.NotEqual(t, a, b)
	require.LessOrEqual(t, len(a), 128)
	require.NotContains(t, a, "-")
	require.NotEmpty(t, termstatus.Report{State: termstatus.StateIdle, ID: a}.Sequence())
}
