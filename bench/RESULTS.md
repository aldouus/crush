# Hill-Climb Results

| # | Title | Target | Before | After | Delta | Verdict |
|---|-------|--------|--------|-------|-------|---------|
| 01 | Lazy gh/gh-stack probes | CLI cold start (`--version`) | 204.9 ms | 112.3 ms | -45% | KEPT |

## Attempt 01: Lazy gh/gh-stack probes

- **Hypothesis**: package-level `ghAvailable`/`ghStackAvailable` in
  `internal/agent/tools/tools.go` run `exec.LookPath("gh")` and
  `gh extension list` during package init, adding ~90 ms to every crush
  invocation, including `--version`.
- **Change**: `internal/agent/tools/tools.go`, `internal/agent/tools/bash.go`.
  Both probes wrapped in `sync.OnceValue`; the only call sites
  (`RenderToolDescription`, bash description render) now call them. Results
  are identical, just computed on first use.
- **Method**: hyperfine `-N --warmup 3 --runs 30`, both binaries back to back,
  HOME/XDG in a temp dir. Load avg 3.77 at start, 3.03 at end.
- **Results** (wall-time median ± stddev):

  | Command | Before | After | Delta |
  |---------|--------|-------|-------|
  | `crush --version` | 204.9 ± 13.3 ms | 112.3 ± 7.9 ms | -45.2% |
  | `crush --help` | 208.9 ± 13.2 ms | 117.7 ± 8.2 ms | -43.7% |
  | `crush dirs` | 247.7 ± 15.6 ms | 164.8 ± 11.4 ms | -33.5% |

  Every median gap is well over 2x stddev. Peak RSS unchanged (about 61 MiB).
- **Tests**: `go test ./internal/agent/tools/`, `go vet`, and golangci-lint
  all pass with 0 issues.
- **Other init candidates** (`GODEBUG=inittrace=1`; none of these spawn a
  subprocess, so nothing was changed for them): `chroma/v2/styles` takes
  about 15 ms and `chroma/v2/lexers` about 14 ms, both building
  registries eagerly. `go-nativeclipboard` takes 3.7 ms. `encoding/gob`
  takes 1.5 ms. The rest are each 1 ms or less.
- **Verdict**: KEPT.
