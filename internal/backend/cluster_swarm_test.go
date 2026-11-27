package backend

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/cluster"
)

type fakeCreateBridge struct {
	fakeBridge
	nodes   []cluster.NodeDashboard
	created []string
	req     SwarmCreateRequest
}

func (f *fakeCreateBridge) Dashboard(context.Context) []cluster.NodeDashboard { return f.nodes }

func (f *fakeCreateBridge) SwarmCreate(_ context.Context, nodeID string, req SwarmCreateRequest) (SwarmLookupResult, error) {
	f.created = append(f.created, nodeID)
	f.req = req
	return SwarmLookupResult{WorkspaceID: "w", SessionID: "s", Color: "red", Animal: "fox"}, nil
}

func TestCreateSwarmSessionOnNode(t *testing.T) {
	b, _ := newTestBackend(t)
	require.False(t, b.ClusterActive())
	_, err := b.CreateSwarmSessionOnNode(t.Context(), "mini1", "/p", "t", "")
	require.ErrorIs(t, err, ErrNoCluster)

	fb := &fakeCreateBridge{nodes: []cluster.NodeDashboard{
		{Member: cluster.Member{NodeID: "self-0000", Name: "laptop"}, Self: true, Online: true},
		{Member: cluster.Member{NodeID: "46e4a1eb-1", Name: "mini1"}, Online: true},
		{Member: cluster.Member{NodeID: "46e4ffff-2", Name: "mini2"}, Online: false},
		{Member: cluster.Member{NodeID: "aaaa-3", Name: "dup"}, Online: true},
		{Member: cluster.Member{NodeID: "bbbb-4", Name: "dup"}, Online: true},
	}}
	b.SetClusterBridge(fb)
	require.True(t, b.ClusterActive())

	res, err := b.CreateSwarmSessionOnNode(t.Context(), "MINI1", "/src/proj", "title", "large")
	require.NoError(t, err)
	require.Equal(t, "46e4a1eb-1", res.NodeID)
	require.Equal(t, []string{"46e4a1eb-1"}, fb.created)
	require.Equal(t, SwarmCreateRequest{Path: "/src/proj", Title: "title", ModelRef: "large"}, fb.req)

	_, err = b.CreateSwarmSessionOnNode(t.Context(), "46e4", "/p", "", "")
	require.ErrorContains(t, err, "ambiguous")

	_, err = b.CreateSwarmSessionOnNode(t.Context(), "46e4ff", "/p", "", "")
	require.ErrorContains(t, err, "offline")

	_, err = b.CreateSwarmSessionOnNode(t.Context(), "nope", "/p", "", "")
	require.ErrorIs(t, err, ErrClusterNodeNotFound)

	_, err = b.CreateSwarmSessionOnNode(t.Context(), "dup", "/p", "", "")
	require.ErrorContains(t, err, "ambiguous")

	_, err = b.CreateSwarmSessionOnNode(t.Context(), "laptop", "relative", "", "")
	require.ErrorIs(t, err, ErrSwarmPathNotDir)
	require.Len(t, fb.created, 1)
}
