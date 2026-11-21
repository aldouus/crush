package prompt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func TestCachedGitStatusMatchesUncached(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	want := getGitStatus(ctx, dir)
	require.Equal(t, want, cachedGitStatus(ctx, dir))
	require.Equal(t, want, cachedGitStatus(ctx, dir))
}

func TestCachedGitStatusInvalidatesOnHeadAndIndex(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	before := cachedGitStatus(ctx, dir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644))
	gitRun(t, dir, "add", "b.txt")
	staged := cachedGitStatus(ctx, dir)
	require.NotEqual(t, before, staged)
	require.Equal(t, getGitStatus(ctx, dir), staged)

	gitRun(t, dir, "commit", "-q", "-m", "second")
	committed := cachedGitStatus(ctx, dir)
	require.Contains(t, committed, "second")
	require.Equal(t, getGitStatus(ctx, dir), committed)

	gitRun(t, dir, "checkout", "-q", "-b", "other")
	require.Contains(t, cachedGitStatus(ctx, dir), "Current branch: other")
}

func TestCachedGitStatusTTL(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	base := time.Now()
	c := newInputCache[string]()
	c.now = func() time.Time { return base }
	calls := 0
	get := func() string {
		return c.get(dir, func() string { return gitFingerprint(dir) }, func() string {
			calls++
			return getGitStatus(ctx, dir)
		})
	}
	get()
	get()
	require.Equal(t, 1, calls)
	base = base.Add(inputCacheTTL)
	get()
	require.Equal(t, 2, calls)
}

func TestCachedDiscoverSkillsInvalidates(t *testing.T) {
	root := t.TempDir()
	writeSkill := func(name string) {
		d := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(d, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(d, "SKILL.md"),
			[]byte("---\nname: "+name+"\ndescription: test skill "+name+"\n---\nbody\n"), 0o644))
	}
	writeSkill("alpha")
	paths := []string{root}
	first := cachedDiscoverSkills(paths)
	require.Len(t, first, 1)
	require.Len(t, cachedDiscoverSkills(paths), 1)

	writeSkill("beta")
	require.Len(t, cachedDiscoverSkills(paths), 2)
}
