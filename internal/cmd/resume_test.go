package cmd

import (
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestResumeCommand(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cmd := &cobra.Command{}
	cmd.Flags().BoolP("debug", "d", false, "")
	cmd.Flags().BoolP("yolo", "y", false, "")
	cmd.Flags().StringP("data-dir", "D", "", "")
	cmd.Flags().String("host", "", "")

	require.NoError(t, cmd.Flags().Set("debug", "true"))
	require.NoError(t, cmd.Flags().Set("yolo", "true"))
	require.NoError(t, cmd.Flags().Set("data-dir", "state"))
	require.NoError(t, cmd.Flags().Set("host", "unix:///tmp/crush.sock"))
	clientHost = "unix:///tmp/crush.sock"
	t.Cleanup(func() { clientHost = "" })

	got := resumeCommand(cmd)
	require.True(t, filepath.IsAbs(got.Args[0]))
	require.Equal(t, []string{"--debug", "--yolo", "--data-dir", filepath.Join(dir, "state"), "--host", "unix:///tmp/crush.sock"}, got.Args[1:])
	require.NotContains(t, got.Args, "--session")
	require.Equal(t, dir, got.Dir)

	plain := &cobra.Command{}
	plain.Flags().BoolP("debug", "d", false, "")
	plain.Flags().BoolP("yolo", "y", false, "")
	plain.Flags().StringP("data-dir", "D", "", "")
	plain.Flags().String("host", "", "")
	require.Len(t, resumeCommand(plain).Args, 1)
}
