package checkpoint

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// ignoreStat records what a stat of a path looked like during a walk so a
// later reuse can detect changes without re-reading directories.
type ignoreStat struct {
	path    string
	exists  bool
	modTime time.Time
	size    int64
}

func statFingerprint(path string) ignoreStat {
	fi, err := os.Stat(path)
	if err != nil {
		return ignoreStat{path: path}
	}
	return ignoreStat{path: path, exists: true, modTime: fi.ModTime(), size: fi.Size()}
}

func (s ignoreStat) fresh() bool {
	return statFingerprint(s.path) == s
}

type ignoreEntry struct {
	patterns []gitignore.Pattern
	stats    []ignoreStat
}

func (e *ignoreEntry) fresh() bool {
	for _, s := range e.stats {
		if !s.fresh() {
			return false
		}
	}
	return true
}

var ignoreCache sync.Map // cleaned absolute work tree -> *ignoreEntry

// readIgnorePatterns returns the same patterns as
// gitignore.ReadPatterns(osfs.New(workTree), nil), reusing a process-wide
// cached result when no walked directory or probed ignore file changed.
func readIgnorePatterns(workTree string) ([]gitignore.Pattern, error) {
	root, err := filepath.Abs(workTree)
	if err != nil || strings.Contains(root, "..") {
		return walkIgnorePatterns(workTree)
	}
	if v, ok := ignoreCache.Load(root); ok {
		e := v.(*ignoreEntry)
		if e.fresh() {
			return e.patterns, nil
		}
	}
	e := &ignoreEntry{}
	ps, err := e.walk(root, nil)
	if err != nil {
		ignoreCache.Delete(root)
		return ps, err
	}
	e.patterns = ps
	ignoreCache.Store(root, e)
	return ps, nil
}

func walkIgnorePatterns(workTree string) ([]gitignore.Pattern, error) {
	e := &ignoreEntry{}
	return e.walk(workTree, nil)
}

func (e *ignoreEntry) readFile(root string, path []string, name string) []gitignore.Pattern {
	full := filepath.Join(append(append([]string{root}, path...), name)...)
	f, err := os.Open(full)
	if err != nil {
		e.stats = append(e.stats, statFingerprint(full))
		return nil
	}
	defer f.Close()
	s := ignoreStat{path: full}
	if fi, err := f.Stat(); err == nil {
		s = ignoreStat{path: full, exists: true, modTime: fi.ModTime(), size: fi.Size()}
	}
	e.stats = append(e.stats, s)
	var ps []gitignore.Pattern
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "#") && len(strings.TrimSpace(line)) > 0 {
			ps = append(ps, gitignore.ParsePattern(line, path))
		}
	}
	return ps
}

// walk mirrors gitignore.ReadPatterns while recording fingerprints.
func (e *ignoreEntry) walk(root string, path []string) ([]gitignore.Pattern, error) {
	ps := e.readFile(root, path, filepath.Join(".git", "info", "exclude"))
	ps = append(ps, e.readFile(root, path, ".gitignore")...)

	dir := filepath.Join(append([]string{root}, path...)...)
	e.stats = append(e.stats, statFingerprint(dir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ps, err
	}
	for _, de := range entries {
		if !de.IsDir() || de.Name() == ".git" {
			continue
		}
		child := append(append([]string(nil), path...), de.Name())
		if gitignore.NewMatcher(ps).Match(child, true) {
			continue
		}
		sub, err := e.walk(root, child)
		if err != nil {
			return ps, err
		}
		ps = append(ps, sub...)
	}
	return ps, nil
}
