package model

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/version"
)

// clusterSelfMsg carries this server's cluster node name ("" when the
// server is not in a cluster).
type clusterSelfMsg struct{ name string }

// fetchClusterSelf asks the attached server which cluster node it is. A
// server that is alone, or not clustered, reports no name.
func (m *UI) fetchClusterSelf() tea.Cmd {
	ws := m.com.Workspace
	if ws == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rows, err := ws.ClusterDashboard(ctx)
		if err != nil || len(rows) < 2 {
			return clusterSelfMsg{}
		}
		for _, r := range rows {
			if r.Self {
				return clusterSelfMsg{name: r.Member.Name}
			}
		}
		return clusterSelfMsg{}
	}
}

func (m *UI) handleClusterSelf(msg clusterSelfMsg) {
	if msg.name == m.clusterSelfName {
		return
	}
	m.clusterSelfName = msg.name
}

// versionLabel is the version shown in the side panel, prefixed with the
// cluster node the open workspace runs on, e.g. "@mini2 v0.95.32".
func (m *UI) versionLabel() string {
	node := ""
	if m.com != nil && m.com.Workspace != nil {
		// NodeName falls back to the raw node id when the name is not
		// known yet; an id is not worth showing.
		if name := m.com.Workspace.NodeName(); name != m.com.Workspace.NodeID() {
			node = name
		}
	}
	if node == "" {
		node = m.clusterSelfName
	}
	return nodeVersionLabel(node, version.Version)
}

func nodeVersionLabel(node, ver string) string {
	if node == "" {
		return ver
	}
	if ver != "" && ver[0] >= '0' && ver[0] <= '9' {
		ver = "v" + ver
	}
	return "@" + strings.TrimSpace(node) + " " + ver
}
