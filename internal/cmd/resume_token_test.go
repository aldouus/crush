package cmd

import (
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestTakeProgramResumeToken(t *testing.T) {
	t.Setenv(ansi.ProgramResumeTokenEnv, "secret-token")
	t.Cleanup(func() { programResumeToken.value, programResumeToken.ok = "", false })

	takeProgramResumeToken()

	_, set := os.LookupEnv(ansi.ProgramResumeTokenEnv)
	require.False(t, set, "token must leave the process environment")
	require.Contains(t, tuiEnviron(), ansi.ProgramResumeTokenEnv+"=secret-token")

	if runtime.GOOS != "windows" {
		// A child spawned with the implicit environment sees no token.
		out, err := exec.CommandContext(t.Context(), "sh", "-c", "env").Output()
		require.NoError(t, err)
		require.NotContains(t, string(out), ansi.ProgramResumeTokenEnv+"=")
	}
}

func TestTuiEnvironWithoutToken(t *testing.T) {
	programResumeToken.value, programResumeToken.ok = "", false
	require.False(t, slices.ContainsFunc(tuiEnviron(), func(kv string) bool {
		return strings.HasPrefix(kv, ansi.ProgramResumeTokenEnv+"=")
	}))
}
