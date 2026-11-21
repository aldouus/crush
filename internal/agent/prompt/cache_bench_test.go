package prompt

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func benchRepo(b *testing.B) string {
	b.Helper()
	dir := b.TempDir()
	benchGit(b, dir, "init", "-q", "-b", "main")
	for i := range 50 {
		_ = os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)+".txt"), []byte("x"), 0o644)
	}
	benchGit(b, dir, "add", ".")
	benchGit(b, dir, "commit", "-q", "-m", "init")
	return dir
}

func benchSkills(b *testing.B) []string {
	b.Helper()
	root := b.TempDir()
	for i := range 20 {
		name := "skill" + strconv.Itoa(i)
		d := filepath.Join(root, name)
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "SKILL.md"),
			[]byte("---\nname: "+name+"\ndescription: bench skill\n---\nbody\n"), 0o644)
	}
	return []string{root}
}

func BenchmarkPromptGitStatusHit(b *testing.B) {
	dir := benchRepo(b)
	ctx := context.Background()
	cachedGitStatus(ctx, dir)
	b.ReportAllocs()
	for b.Loop() {
		cachedGitStatus(ctx, dir)
	}
}

func BenchmarkPromptGitStatusMissTTL(b *testing.B) {
	dir := benchRepo(b)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		expirePromptInputCaches()
		cachedGitStatus(ctx, dir)
	}
}

func BenchmarkPromptGitStatusParallelColdBurst(b *testing.B) {
	dir := benchRepo(b)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		expirePromptInputCaches()
		done := make(chan struct{}, 8)
		for range 8 {
			go func() {
				cachedGitStatus(ctx, dir)
				done <- struct{}{}
			}()
		}
		for range 8 {
			<-done
		}
	}
}

func BenchmarkPromptSkillsHit(b *testing.B) {
	paths := benchSkills(b)
	cachedDiscoverSkills(paths)
	b.ReportAllocs()
	for b.Loop() {
		cachedDiscoverSkills(paths)
	}
}

func BenchmarkPromptSkillsMissTTL(b *testing.B) {
	paths := benchSkills(b)
	b.ReportAllocs()
	for b.Loop() {
		expirePromptInputCaches()
		cachedDiscoverSkills(paths)
	}
}
