package backend

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/home"
)

// SwarmCreateBridge is implemented by cluster bridges that can create a
// swarm session on another node.
type SwarmCreateBridge interface {
	SwarmCreate(ctx context.Context, nodeID string, req SwarmCreateRequest) (SwarmLookupResult, error)
}

// SwarmCreateRequest asks a node to create a session in the workspace
// rooted at Path on that node.
type SwarmCreateRequest struct {
	Path     string `json:"path"`
	Title    string `json:"title,omitempty"`
	ModelRef string `json:"model_ref,omitempty"`
}

// ErrClusterNodeNotFound is returned when a node reference matches no
// cluster member.
var ErrClusterNodeNotFound = errors.New("no cluster node matches")

// ClusterActive reports whether this server is attached to a cluster
// mesh.
func (b *Backend) ClusterActive() bool {
	return b.clusterBridge() != nil
}

// ClusterNodes returns live health for every cluster member, this node
// included.
func (b *Backend) ClusterNodes(ctx context.Context) ([]cluster.NodeDashboard, error) {
	db, ok := b.clusterBridge().(DashboardBridge)
	if !ok {
		return nil, ErrNoCluster
	}
	return db.Dashboard(ctx), nil
}

// ResolveClusterNode matches ref against member names, node IDs, and
// node ID prefixes (case-insensitive).
func (b *Backend) ResolveClusterNode(ctx context.Context, ref string) (cluster.NodeDashboard, error) {
	nodes, err := b.ClusterNodes(ctx)
	if err != nil {
		return cluster.NodeDashboard{}, err
	}
	return matchClusterNode(nodes, ref)
}

func matchClusterNode(nodes []cluster.NodeDashboard, ref string) (cluster.NodeDashboard, error) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" {
		return cluster.NodeDashboard{}, fmt.Errorf("%w: empty node", ErrClusterNodeNotFound)
	}
	for _, n := range nodes {
		if strings.ToLower(n.Member.NodeID) == ref {
			return n, nil
		}
	}
	var named []cluster.NodeDashboard
	for _, n := range nodes {
		if strings.ToLower(n.Member.Name) == ref {
			named = append(named, n)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
	default:
		return cluster.NodeDashboard{}, fmt.Errorf("node name %q is ambiguous; use a node id", ref)
	}
	var matches []cluster.NodeDashboard
	for _, n := range nodes {
		if strings.HasPrefix(strings.ToLower(n.Member.NodeID), ref) {
			matches = append(matches, n)
		}
	}
	switch len(matches) {
	case 0:
		return cluster.NodeDashboard{}, fmt.Errorf("%w %q", ErrClusterNodeNotFound, ref)
	case 1:
		return matches[0], nil
	default:
		return cluster.NodeDashboard{}, fmt.Errorf("node %q is ambiguous", ref)
	}
}

// CreateSwarmSessionOnNode creates a session in the workspace rooted at
// path on the given cluster node (name, ID, or ID prefix). The path is
// resolved on that node, not here. This node is handled locally.
func (b *Backend) CreateSwarmSessionOnNode(ctx context.Context, nodeRef, path, title, modelRef string) (SwarmLookupResult, error) {
	node, err := b.ResolveClusterNode(ctx, nodeRef)
	if err != nil {
		return SwarmLookupResult{}, err
	}
	if node.Self {
		return b.HandleRemoteSwarmCreate(ctx, SwarmCreateRequest{Path: path, Title: title, ModelRef: modelRef})
	}
	if !node.Online {
		return SwarmLookupResult{}, fmt.Errorf("cluster node %s is offline", node.Member.Name)
	}
	cb, ok := b.clusterBridge().(SwarmCreateBridge)
	if !ok {
		return SwarmLookupResult{}, ErrNoCluster
	}
	res, err := cb.SwarmCreate(ctx, node.Member.NodeID, SwarmCreateRequest{
		Path:     path,
		Title:    title,
		ModelRef: modelRef,
	})
	if err != nil {
		return SwarmLookupResult{}, err
	}
	res.NodeID = node.Member.NodeID
	return res, nil
}

// HandleRemoteSwarmCreate serves a [SwarmCreateRequest] by creating the
// session on this node. Any cluster member may call it: membership is
// the trust boundary, as for session moves. The path must be an
// absolute, existing directory.
func (b *Backend) HandleRemoteSwarmCreate(ctx context.Context, req SwarmCreateRequest) (SwarmLookupResult, error) {
	if !filepath.IsAbs(home.Expand(req.Path)) {
		return SwarmLookupResult{}, fmt.Errorf("%w: path must be absolute: %q", ErrSwarmPathNotDir, req.Path)
	}
	wsID, sess, err := b.CreateSwarmSessionAtPath(ctx, req.Path, req.Title, req.ModelRef)
	if err != nil {
		return SwarmLookupResult{}, err
	}
	res := SwarmLookupResult{
		WorkspaceID: wsID,
		SessionID:   sess.ID,
		Color:       sess.Color,
		Animal:      sess.Animal,
		NodeID:      b.selfNodeID(),
	}
	if ws, ok := b.workspaces.Get(wsID); ok {
		res.WorkspaceRoot = ws.resolvedPath
	}
	return res, nil
}
