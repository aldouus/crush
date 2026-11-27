package shell

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithoutProgramResumeToken(t *testing.T) {
	t.Parallel()

	in := []string{"PATH=/bin", "OSC7577_TOKEN=secret", "HOME=/home"}
	got := WithoutProgramResumeToken(in)

	require.Equal(t, []string{"PATH=/bin", "HOME=/home"}, got)
	require.Equal(t, []string{"PATH=/bin", "OSC7577_TOKEN=secret", "HOME=/home"}, in, "input must not be mutated")

	// A name that merely starts with the token's name is a different
	// variable and must survive.
	prefixed := WithoutProgramResumeToken([]string{"OSC7577_TOKEN_EXTRA=keep"})
	require.Equal(t, []string{"OSC7577_TOKEN_EXTRA=keep"}, prefixed)
}

func TestShellDropsProgramResumeToken(t *testing.T) {
	t.Setenv("OSC7577_TOKEN", "from-process")

	t.Run("inherited", func(t *testing.T) {
		sh := NewShell(&Options{WorkingDir: t.TempDir()})
		// printenv is external, so this reads the environment a child gets.
		out, _, err := sh.Exec(context.Background(), "printenv OSC7577_TOKEN || true")
		require.NoError(t, err)
		require.Empty(t, strings.TrimSpace(out))
		require.NotContains(t, strings.Join(sh.GetEnv(), "\n"), "OSC7577_TOKEN=")
	})

	t.Run("supplied", func(t *testing.T) {
		sh := NewShell(&Options{
			WorkingDir: t.TempDir(),
			Env:        []string{"OSC7577_TOKEN=from-caller", "KEEP=yes"},
		})
		out, _, err := sh.Exec(context.Background(), "printf %s \"$KEEP$OSC7577_TOKEN\"")
		require.NoError(t, err)
		require.Equal(t, "yes", out)
		require.NotContains(t, strings.Join(sh.GetEnv(), "\n"), "OSC7577_TOKEN=")
	})
}

func TestRunDropsProgramResumeToken(t *testing.T) {
	var stdout strings.Builder
	err := Run(context.Background(), RunOptions{
		Command: "printf %s \"$KEEP$OSC7577_TOKEN\"",
		Cwd:     t.TempDir(),
		Env:     []string{"OSC7577_TOKEN=from-caller", "KEEP=yes"},
		Stdout:  &stdout,
	})
	require.NoError(t, err)
	require.Equal(t, "yes", stdout.String())
}
