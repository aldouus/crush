package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/workspace"
)

func withResume(args ...string) *UI {
	return &UI{resume: struct {
		args []string
		dir  string
	}{args: args, dir: "/work"}}
}

func TestProgramResume(t *testing.T) {
	t.Parallel()

	t.Run("session", func(t *testing.T) {
		t.Parallel()
		m := withResume("/usr/bin/crush", "--debug")
		m.session = &session.Session{ID: "sess-1"}

		got := m.programResume()
		require.Equal(t, []string{"/usr/bin/crush", "--debug", "--session", "sess-1"}, got.Args)
		require.Equal(t, "/work", got.Dir)
	})

	t.Run("no session", func(t *testing.T) {
		t.Parallel()
		got := withResume("/usr/bin/crush").programResume()
		require.Equal(t, []string{"/usr/bin/crush"}, got.Args)
		require.Equal(t, "/work", got.Dir)
	})

	t.Run("quit clears", func(t *testing.T) {
		t.Parallel()
		m := withResume("/usr/bin/crush")
		m.session = &session.Session{ID: "sess-1"}
		m.quitting = true
		require.Nil(t, m.programResume())
	})

	t.Run("unreportable", func(t *testing.T) {
		t.Parallel()
		m := &UI{session: &session.Session{ID: "sess-1"}}
		require.Nil(t, m.programResume())
	})

	t.Run("launch dir wins over project root", func(t *testing.T) {
		t.Parallel()
		m := withResume("/usr/bin/crush")
		m.com = &common.Common{Workspace: dirWorkspace{base: "/repo", launch: "/repo-feature"}}
		require.Equal(t, "/repo-feature", m.programResume().Dir)
	})

	t.Run("active worktree wins over launch dir", func(t *testing.T) {
		t.Parallel()
		m := withResume("/usr/bin/crush")
		m.com = &common.Common{Workspace: dirWorkspace{base: "/repo", launch: "/repo", active: "/repo/.crush/worktrees/feature"}}
		require.Equal(t, "/repo/.crush/worktrees/feature", m.programResume().Dir)
	})
}

type dirWorkspace struct {
	workspace.Workspace
	base, launch, active string
}

func (w dirWorkspace) BaseDir() string             { return w.base }
func (w dirWorkspace) EffectiveWorkingDir() string { return w.launch }
func (w dirWorkspace) WorkingDir() string {
	if w.active != "" {
		return w.active
	}
	return w.launch
}

func TestProgramResumeTracksSession(t *testing.T) {
	t.Parallel()

	m := withResume("/usr/bin/crush")
	m.session = &session.Session{ID: "first"}
	require.Equal(t, "first", m.programResume().Args[2])

	m.session.ID = "second"
	require.Equal(t, "second", m.programResume().Args[2])

	m.session = nil
	require.Equal(t, []string{"/usr/bin/crush"}, m.programResume().Args)
}
