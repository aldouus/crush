package cluster

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newGossipNode is a test node that gossips quickly and never retries
// announcements, so only gossip can repair a missed one.
func newGossipNode(t *testing.T) *Node {
	t.Helper()
	n := newTestNode(t, "v1.0.0")
	n.gossipInterval = 50 * time.Millisecond
	n.broadcastRetryFor = 0
	return n
}

func memberIDs(t *testing.T, n *Node) []string {
	t.Helper()
	st, err := n.Store().Snapshot()
	if err != nil {
		return nil
	}
	var ids []string
	for _, m := range st.Members {
		ids = append(ids, m.NodeID)
	}
	return ids
}

func hasMember(t *testing.T, n *Node, id string) bool {
	t.Helper()
	for _, m := range memberIDs(t, n) {
		if m == id {
			return true
		}
	}
	return false
}

// startCluster creates a, b with b joined through a.
func startCluster(t *testing.T, ctx context.Context) (a, b *Node, token string, bAddr string) {
	t.Helper()
	a = newGossipNode(t)
	b = newGossipNode(t)
	aAddr := freeAddr(t)
	token, err := a.Init([]Endpoint{{Kind: EndpointDirect, Addr: aAddr}})
	require.NoError(t, err)
	require.NoError(t, a.Start(aAddr))
	joinAndApprove(t, ctx, b, a, token)
	return a, b, token, b.Listening()
}

func TestGossipRepairsMissedApproveThroughThirdMember(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	a, b, token, bAddr := startCluster(t, ctx)
	// B is offline while A approves C, so A's announcement misses it.
	require.NoError(t, b.Stop(ctx))
	c := newGossipNode(t)
	joinAndApprove(t, ctx, c, a, token)
	require.False(t, hasMember(t, b, c.Identity().ID))
	// C is never reachable from B: B can only learn of it through A.
	require.NoError(t, c.Stop(ctx))

	require.NoError(t, b.Start(bAddr))
	require.Eventually(t, func() bool { return hasMember(t, b, c.Identity().ID) }, 10*time.Second, 20*time.Millisecond)
	require.True(t, b.GossipSynced())

	st := mustSnapshot(t, b)
	got, _ := st.Member(c.Identity().ID)
	require.Equal(t, c.Identity().PublicKey(), ed25519.PublicKey(got.PublicKey))
	require.Nil(t, got.Device, "device records are never relayed")
}

func TestGossipKeepsPinnedKey(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	a, b, _, _ := startCluster(t, ctx)
	st := mustSnapshot(t, a)
	from, _ := st.Member(b.Identity().ID)

	// A peer gossips a known member ID with another key.
	other, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	forged := Member{NodeID: b.Identity().ID, Name: "forged", PublicKey: other, JoinedAt: time.Now()}
	err = a.mergeGossip(from, membersBody{Members: []Member{forged}})
	require.ErrorContains(t, err, "pinned to a different key")

	after, _ := mustSnapshot(t, a).Member(b.Identity().ID)
	require.Equal(t, from.PublicKey, after.PublicKey)
	require.NotEqual(t, "forged", after.Name)
}

func TestGossipDoesNotResurrectDepartedMember(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	a, b, token, bAddr := startCluster(t, ctx)
	c := newGossipNode(t)
	joinAndApprove(t, ctx, c, a, token)
	for _, n := range []*Node{a, b} {
		require.Eventually(t, func() bool { return hasMember(t, n, c.Identity().ID) }, 10*time.Second, 20*time.Millisecond)
	}

	// B misses C's leave and keeps gossiping C to A.
	require.NoError(t, b.Stop(ctx))
	cID := c.Identity().ID
	c.Leave(ctx)
	require.False(t, hasMember(t, a, cID))
	require.True(t, a.Departed(cID))
	require.True(t, hasMember(t, b, cID))

	require.NoError(t, b.Start(bAddr))
	require.Eventually(t, func() bool { return !hasMember(t, b, cID) }, 10*time.Second, 20*time.Millisecond)
	require.True(t, b.Departed(cID))
	// Several more rounds: C stays gone on both.
	time.Sleep(300 * time.Millisecond)
	require.False(t, hasMember(t, a, cID))
	require.False(t, hasMember(t, b, cID))
}

func TestBroadcastRetryDeliversAfterOutage(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	a := newGossipNode(t)
	a.broadcastRetryFor = 10 * time.Second
	b := newGossipNode(t)
	// Gossip is off on B, so only A's retried announcement can reach it.
	b.gossipInterval = -1
	aAddr := freeAddr(t)
	token, err := a.Init([]Endpoint{{Kind: EndpointDirect, Addr: aAddr}})
	require.NoError(t, err)
	require.NoError(t, a.Start(aAddr))
	joinAndApprove(t, ctx, b, a, token)
	bAddr := b.Listening()
	require.NoError(t, b.Stop(ctx))
	c := newGossipNode(t)
	joinAndApprove(t, ctx, c, a, token)
	require.NoError(t, b.Start(bAddr))
	require.Eventually(t, func() bool { return hasMember(t, b, c.Identity().ID) }, 10*time.Second, 20*time.Millisecond)
}
