package backend

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/proto"
)

// peekCache memoizes read-only session listings of detached workspaces,
// keyed by the database files' size and modification time. Listing every
// known project used to reopen each database on every sidebar refresh,
// which costs hundreds of milliseconds once a user has many projects.
//
// Every read runs under a [stallGuard], so a database whose open never
// returns marks that one workspace unavailable instead of wedging the
// whole overview.
type peekCache struct {
	mu      sync.Mutex
	entries map[string]peekEntry
	guard   stallGuard
	// peek lists a data directory's sessions; nil means [db.PeekSessions].
	peek func(ctx context.Context, dataDir string) ([]db.PeekedSession, error)
}

type peekEntry struct {
	stamp    string
	sessions []db.PeekedSession
	// overviews is sessions converted and sorted for the overview, built
	// once per stamp. Shared by every caller: read-only.
	overviews []proto.SessionOverview
	at        time.Time
}

// dbStamp fingerprints a data directory's database files; any write
// changes the WAL or the main file.
func dbStamp(dataDir string) string {
	var stamp string
	for _, name := range []string{"crush.db", "crush.db-wal"} {
		fi, err := os.Stat(filepath.Join(dataDir, name))
		if err != nil {
			stamp += name + ":-;"
			continue
		}
		stamp += name + ":" + fi.ModTime().UTC().Format(time.RFC3339Nano) + ":" + strconv.FormatInt(fi.Size(), 10) + ";"
	}
	return stamp
}

func (c *peekCache) get(ctx context.Context, dataDir string) ([]db.PeekedSession, error) {
	return guarded(ctx, &c.guard, filepath.Clean(dataDir), func(ctx context.Context) ([]db.PeekedSession, error) {
		return c.load(ctx, dataDir)
	})
}

func (c *peekCache) load(ctx context.Context, dataDir string) ([]db.PeekedSession, error) {
	e, err := c.loadEntry(ctx, dataDir)
	return e.sessions, err
}

// overviews returns dataDir's sessions in overview display order. When
// the read fails but an earlier listing is cached, that listing is
// returned with stale set (and the error), so a slow or contended read
// degrades to old data instead of an empty workspace.
func (c *peekCache) overviews(ctx context.Context, dataDir string) (ovs []proto.SessionOverview, staleSince time.Time, err error) {
	e, err := guarded(ctx, &c.guard, filepath.Clean(dataDir), func(ctx context.Context) (peekEntry, error) {
		return c.loadEntry(ctx, dataDir)
	})
	if err == nil {
		return e.overviews, time.Time{}, nil
	}
	c.mu.Lock()
	prev, ok := c.entries[dataDir]
	c.mu.Unlock()
	if !ok {
		return nil, time.Time{}, err
	}
	return prev.overviews, prev.at, err
}

func (c *peekCache) loadEntry(ctx context.Context, dataDir string) (peekEntry, error) {
	stamp := dbStamp(dataDir)
	c.mu.Lock()
	if e, ok := c.entries[dataDir]; ok && e.stamp == stamp {
		c.mu.Unlock()
		return e, nil
	}
	c.mu.Unlock()
	peek := c.peek
	if peek == nil {
		peek = db.PeekSessions
	}
	sessions, err := peek(ctx, dataDir)
	if err != nil {
		return peekEntry{}, err
	}
	e := peekEntry{stamp: stamp, sessions: sessions, overviews: peekedOverviews(sessions), at: time.Now()}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]peekEntry{}
	}
	c.entries[dataDir] = e
	c.mu.Unlock()
	return e, nil
}

// peekedOverviews converts a detached workspace's sessions to sorted
// overview entries.
func peekedOverviews(peeked []db.PeekedSession) []proto.SessionOverview {
	if len(peeked) == 0 {
		return nil
	}
	out := make([]proto.SessionOverview, 0, len(peeked))
	for _, p := range peeked {
		out = append(out, proto.SessionOverview{
			ID:         p.ID,
			Title:      p.Title,
			WorkingDir: p.WorkingDir,
			UpdatedAt:  p.UpdatedAt,
			IsBusy:     false, // not attached: no live state
			Unread:     p.Unread(),
			Color:      p.Color,
			Animal:     p.Animal,
			Favorite:   p.Favorite,
			OwnerNode:  p.OwnerNode,
		})
	}
	sortSessionOverviews(out)
	return out
}

// reset drops every memoized listing.
func (c *peekCache) reset() {
	c.mu.Lock()
	c.entries = nil
	c.mu.Unlock()
}
