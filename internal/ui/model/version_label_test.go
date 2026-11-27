package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/cluster"
)

func TestNodeVersionLabel(t *testing.T) {
	t.Parallel()
	require.Equal(t, "v0.95.32", nodeVersionLabel("", "v0.95.32"))
	require.Equal(t, "@mini2 v0.95.32", nodeVersionLabel("mini2", "v0.95.32"))
	require.Equal(t, "@mini2 v0.95.32", nodeVersionLabel("mini2", "0.95.32"))
	require.Equal(t, "@mini2 devel", nodeVersionLabel("mini2", "devel"))
}

type nodeWorkspace struct {
	sendWorkspace
	node string
	id   string
	rows []cluster.NodeDashboard
}

func (w *nodeWorkspace) NodeName() string { return w.node }

func TestFetchClusterSelf(t *testing.T) {
	t.Parallel()
	self := cluster.NodeDashboard{Self: true, Member: cluster.Member{Name: "mini2"}}
	peer := cluster.NodeDashboard{Member: cluster.Member{Name: "big"}}

	for name, tc := range map[string]struct {
		rows []cluster.NodeDashboard
		want string
	}{
		"clustered": {rows: []cluster.NodeDashboard{peer, self}, want: "mini2"},
		"alone":     {rows: []cluster.NodeDashboard{self}, want: ""},
		"none":      {want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			u := newTestUI()
			u.com.Workspace = &dashWorkspace{rows: tc.rows}
			require.Equal(t, clusterSelfMsg{name: tc.want}, u.fetchClusterSelf()())
		})
	}
}

func TestVersionLabelPrefersTargetNode(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.clusterSelfName = "mini2"
	u.com.Workspace = &nodeWorkspace{}
	require.Contains(t, u.versionLabel(), "@mini2 ")
	u.com.Workspace = &nodeWorkspace{node: "big"}
	require.Contains(t, u.versionLabel(), "@big ")
}

type dashWorkspace struct {
	sendWorkspace
	rows []cluster.NodeDashboard
}

func (w *dashWorkspace) ClusterDashboard(context.Context) ([]cluster.NodeDashboard, error) {
	return w.rows, nil
}

func (w *testWorkspace) NodeName() string { return "" }

func (w *sendWorkspace) NodeName() string { return "" }

func (w *testWorkspace) NodeID() string { return "" }

func (w *sendWorkspace) NodeID() string { return "" }

func (w *nodeWorkspace) NodeID() string { return w.id }

func TestVersionLabelHidesRawNodeID(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.com.Workspace = &nodeWorkspace{node: "3f9c-id", id: "3f9c-id"}
	require.NotContains(t, u.versionLabel(), "@")
}

func TestSidebarLogoFollowsNodeSwitch(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	ws := &nodeWorkspace{node: "mini2"}
	u.com.Workspace = ws
	u.cacheSidebarLogo(40)
	require.Equal(t, u.versionLabel(), u.sidebarLogoLabel)
	require.Contains(t, u.sidebarLogoLabel, "@mini2")

	ws.node = "big"
	require.NotEqual(t, u.versionLabel(), u.sidebarLogoLabel, "draw must notice the stale label")
}
