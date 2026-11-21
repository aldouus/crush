package backend

import (
	"os"
	"path/filepath"
	"strings"
)

// readGitBranch resolves the branch `git branch --show-current` would
// print for dir by reading HEAD directly, avoiding a git subprocess on
// every workspace open. ok is false whenever the layout is anything but
// the plain cases (env overrides, bare repos, foreign ownership, unusual
// HEAD contents), in which case the caller must ask git.
func readGitBranch(dir string) (branch string, ok bool) {
	for _, env := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM", "GIT_CONFIG_PARAMETERS"} {
		if _, set := os.LookupEnv(env); set {
			return "", false
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	cur := abs
	for {
		if isGitDir(cur) {
			return "", false
		}
		fi, err := os.Lstat(filepath.Join(cur, ".git"))
		if err == nil {
			return branchFromDotGit(cur, fi)
		}
		if !os.IsNotExist(err) {
			return "", false
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", true
		}
		cur = parent
	}
}

func isGitDir(dir string) bool {
	if _, err := os.Lstat(filepath.Join(dir, "HEAD")); err != nil {
		return false
	}
	return true
}

func branchFromDotGit(top string, fi os.FileInfo) (string, bool) {
	if !ownedByMe(top) {
		return "", false
	}
	var gitDir string
	switch {
	case fi.IsDir():
		gitDir = filepath.Join(top, ".git")
	case fi.Mode().IsRegular():
		data, err := os.ReadFile(filepath.Join(top, ".git"))
		if err != nil {
			return "", false
		}
		rest, found := strings.CutPrefix(string(data), "gitdir: ")
		if !found {
			return "", false
		}
		gitDir = strings.TrimRight(rest, "\r\n")
		if gitDir == "" || strings.ContainsAny(gitDir, "\r\n") {
			return "", false
		}
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(top, gitDir)
		}
	default:
		return "", false
	}
	if !ownedByMe(gitDir) {
		return "", false
	}
	headPath := filepath.Join(gitDir, "HEAD")
	hfi, err := os.Lstat(headPath)
	if err != nil || !hfi.Mode().IsRegular() {
		return "", false
	}
	data, err := os.ReadFile(headPath)
	if err != nil {
		return "", false
	}
	head, found := strings.CutSuffix(string(data), "\n")
	if !found || strings.ContainsAny(head, "\r\n") {
		return "", false
	}
	if name, isRef := strings.CutPrefix(head, "ref: refs/heads/"); isRef {
		if name == "" || name == ".invalid" || strings.ContainsAny(name, " \t") {
			return "", false
		}
		return name, true
	}
	if isHexObjectID(head) {
		return "", true
	}
	return "", false
}

func isHexObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
