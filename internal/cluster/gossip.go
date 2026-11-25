package cluster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"
)

const (
	// defaultGossipInterval is the pause between membership gossip
	// rounds. Membership changes rarely; announcements carry them at
	// once and gossip only repairs what an announcement missed.
	defaultGossipInterval = 30 * time.Second
	// gossipTimeout bounds one peer's part of a gossip round.
	gossipTimeout = 10 * time.Second
	// departureTTL is how long a leave is remembered. Gossip converges
	// long before; afterwards a stale record could only come from a node
	// offline for longer than this.
	departureTTL = 30 * 24 * time.Hour

	// broadcastTimeout bounds one member's part of a broadcast.
	broadcastTimeout = 10 * time.Second
	// defaultBroadcastRetryFor bounds how long a failed announcement is
	// retried in the background.
	defaultBroadcastRetryFor = 10 * time.Minute
	broadcastRetryMin        = 2 * time.Second
	broadcastRetryMax        = time.Minute
)

// errNoChange aborts a store update that would not change anything, so
// idle gossip never rewrites cluster.json or wakes watchers.
var errNoChange = errors.New("no change")

// lifetime returns the context background work should run under: the
// listener's lifetime, or a background context when not listening.
func (n *Node) lifetime() context.Context {
	n.srvMu.Lock()
	defer n.srvMu.Unlock()
	if n.life != nil {
		return n.life
	}
	return context.Background()
}

// GossipSynced reports whether this node has completed a membership
// gossip round with at least one peer since it started listening (or
// has no peers). Until then its member list may lag the cluster's.
func (n *Node) GossipSynced() bool { return n.gossipSynced.Load() }

// Departed reports whether nodeID is known to have left the cluster.
func (n *Node) Departed(nodeID string) bool {
	st, err := n.store.Snapshot()
	return err == nil && st.HasDeparted(nodeID)
}

// gossipLoop pulls every peer's member list now and then every
// gossipInterval, until ctx ends. Pulling (rather than only being told)
// lets a member that missed an announcement learn of a new member from
// any third member, even one the new member can never reach directly.
func (n *Node) gossipLoop(ctx context.Context, interval time.Duration) {
	if interval < 0 {
		return
	}
	interval = cmpDuration(interval, defaultGossipInterval)
	for {
		n.gossipRound(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// gossipRound asks each other member for its member list and merges
// what it learns.
func (n *Node) gossipRound(ctx context.Context) {
	peers := n.otherMembers()
	if len(peers) == 0 {
		if n.store.InCluster() {
			n.gossipSynced.Store(true)
		}
		return
	}
	for _, m := range peers {
		if ctx.Err() != nil {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, gossipTimeout)
		var body membersBody
		err := n.callMember(cctx, m, http.MethodGet, "/cluster/v1/members", nil, &body)
		cancel()
		if err != nil {
			// Older members do not serve gossip; unreachable ones are
			// covered by the others.
			slog.Debug("Cluster gossip failed", "member", m.Name, "error", err)
			continue
		}
		if err := n.mergeGossip(m, body); err != nil {
			slog.Warn("Cluster gossip merge failed", "member", m.Name, "error", err)
			continue
		}
		n.gossipSynced.Store(true)
	}
}

// handleGossip serves this node's member list and departures.
func (n *Node) handleGossip(w http.ResponseWriter, _ *http.Request, _ Member) {
	st, err := n.store.Snapshot()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	for i := range st.Members {
		// Last-seen times are this node's own observations.
		st.Members[i].LastSeen = time.Time{}
	}
	writeJSON(w, http.StatusOK, membersBody{Members: st.Members, Departed: st.Departed})
}

// mergeGossip merges a peer's member list into the store with the same
// rules as an announcement ([Node.handleMembers]): self is skipped, a
// pinned key is never replaced, departed members stay departed, and
// device records are accepted only from their own member. Unlike an
// announcement, a conflicting entry is skipped rather than failing the
// whole list, and the endpoints of members already known are kept
// (they are this node's own view of how to reach them).
func (n *Node) mergeGossip(from Member, body membersBody) error {
	var conflicts []error
	err := n.store.Update(func(st *State) error {
		before := cloneState(*st)
		for _, d := range body.Departed {
			if d.NodeID == "" || d.NodeID == n.id.ID {
				continue
			}
			st.recordDeparture(d.NodeID, d.LeftAt)
		}
		for _, m := range body.Members {
			if m.NodeID == "" || len(m.PublicKey) == 0 || m.NodeID == n.id.ID || st.departed(m) {
				continue
			}
			existing, known := st.Member(m.NodeID)
			if known && !samePublicKey(existing.PublicKey, m.PublicKey) {
				conflicts = append(conflicts, fmt.Errorf("member %s is pinned to a different key", m.NodeID))
				continue
			}
			if known {
				existing.Device = mergeDevice(m.NodeID, from.NodeID, existing.Device, m.Device)
				existing.Version = gossipVersion(existing.Version, m.Version)
				st.upsertMember(existing)
			} else {
				m.Device = mergeDevice(m.NodeID, from.NodeID, nil, m.Device)
				m.LastSeen = time.Time{}
				st.upsertMember(m)
				slog.Info("Cluster member learned via gossip", "node", m.Name, "node_id", m.NodeID, "from", from.Name)
			}
			st.Pending = slices.DeleteFunc(st.Pending, func(p JoinRequest) bool { return p.NodeID == m.NodeID })
		}
		st.pruneDepartures(time.Now().Add(-departureTTL))
		if stateEqual(before, *st) {
			return errNoChange
		}
		return nil
	})
	if errors.Is(err, errNoChange) {
		err = nil
	}
	return errors.Join(append([]error{err}, conflicts...)...)
}

// stateEqual reports whether two states hold the same membership.
func stateEqual(a, b State) bool {
	return slices.EqualFunc(a.Members, b.Members, memberEqual) &&
		slices.EqualFunc(a.Departed, b.Departed, func(x, y Departure) bool { return x.NodeID == y.NodeID && x.LeftAt.Equal(y.LeftAt) }) &&
		slices.EqualFunc(a.Pending, b.Pending, func(x, y JoinRequest) bool { return x.ID == y.ID })
}

func memberEqual(a, b Member) bool {
	return a.NodeID == b.NodeID && a.Name == b.Name && samePublicKey(a.PublicKey, b.PublicKey) &&
		slices.Equal(a.Endpoints, b.Endpoints) && a.Version == b.Version &&
		a.JoinedAt.Equal(b.JoinedAt) && a.LastSeen.Equal(b.LastSeen) &&
		a.Device.SameListeners(b.Device) && (a.Device == nil || a.Device.Seq == b.Device.Seq)
}
