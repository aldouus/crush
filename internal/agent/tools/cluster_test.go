package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"
)

type stubClusterBackend struct {
	active bool
	nodes  []ClusterNode
}

func (s *stubClusterBackend) ClusterActive() bool { return s.active }

func (s *stubClusterBackend) ClusterNodes(context.Context) ([]ClusterNode, error) {
	return s.nodes, nil
}

func (s *stubClusterBackend) CreateSessionOnNode(context.Context, string, string, string, string) (SwarmLookupResult, error) {
	return SwarmLookupResult{}, nil
}

func TestClusterToolGatedOnMembership(t *testing.T) {
	t.Parallel()

	cb := &stubClusterBackend{}
	tool := NewClusterTool(nil, cb, nil, nil, "")
	gated, ok := tool.(ContextAvailability)
	require.True(t, ok)
	require.False(t, gated.AvailableInContext(t.Context()))
	require.Empty(t, FilterAvailableTools(t.Context(), []fantasy.AgentTool{tool}))

	cb.active = true
	require.True(t, gated.AvailableInContext(t.Context()))
	require.Len(t, FilterAvailableTools(t.Context(), []fantasy.AgentTool{tool}), 1)
}

func TestFormatClusterNodes(t *testing.T) {
	t.Parallel()

	out := formatClusterNodes([]ClusterNode{
		{NodeID: "6909e49d-aaaa", Name: "mini2", Online: true, RTTMillis: 3},
		{NodeID: "fb31538f-bbbb", Name: "laptop", Online: true, Self: true, RTTMillis: -1},
		{
			NodeID: "46e4a1eb-cccc", Name: "mini1", Online: true, RTTMillis: 2,
			HasStats: true, Hostname: "mini1.lan", Platform: "darwin/arm64", CPUCores: 10, FreeScore: 0.8,
			Jobs: []ClusterJob{{SessionID: "s1", Title: "tests", WorkspaceRoot: "/src/crush", Color: "seagreen", Animal: "impala"}},
		},
	})
	require.Contains(t, out, "3 cluster node(s)")
	require.Contains(t, out, "laptop (id fb31538f, online, this node)")
	require.Contains(t, out, "running: seagreen-impala")
	require.Less(t, strings.Index(out, "laptop"), strings.Index(out, "mini1"))
	require.Less(t, strings.Index(out, "mini1"), strings.Index(out, "mini2"))
}

func TestPickAutoNode(t *testing.T) {
	t.Parallel()

	best, ok := pickAutoNode([]ClusterNode{
		{NodeID: "a", Online: true, HasStats: true, FreeScore: 0.5},
		{NodeID: "self", Self: true, Online: true, HasStats: true, FreeScore: 0.5},
		{NodeID: "b", Online: false, HasStats: true, FreeScore: 0.9},
		{NodeID: "c", Online: true, HasStats: true, FreeScore: 0.3},
		{NodeID: "d", Online: true, FreeScore: 0.99},
	})
	require.True(t, ok)
	require.Equal(t, "a", best.NodeID)

	_, ok = pickAutoNode(nil)
	require.False(t, ok)
}
