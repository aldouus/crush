package tools

import (
	"cmp"
	"context"
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/swarm"
)

// ClusterToolName is the tool identifier exposed to the model.
const ClusterToolName = "cluster"

//go:embed cluster.md
var clusterToolDescription string

// ClusterBackend is what the cluster tool needs beyond [SwarmBackend].
type ClusterBackend interface {
	// ClusterActive reports whether this server is attached to a cluster
	// mesh right now. The tool is only advertised while it is true.
	ClusterActive() bool
	// ClusterNodes returns every member, this node included.
	ClusterNodes(ctx context.Context) ([]ClusterNode, error)
	// CreateSessionOnNode creates a session in the workspace rooted at
	// path on node (name, ID, or ID prefix). path is resolved on that
	// node.
	CreateSessionOnNode(ctx context.Context, node, path, title, modelRef string) (SwarmLookupResult, error)
}

// ClusterNode is a cluster member with its latest health sample.
type ClusterNode struct {
	NodeID     string
	Name       string
	Version    string
	Self       bool
	Online     bool
	RTTMillis  int64
	HasStats   bool
	Platform   string
	Hostname   string
	CPUPercent float64
	CPUCores   int
	MemPercent float64
	Sessions   int
	FreeScore  float64
	Jobs       []ClusterJob
}

// ClusterJob is a session with an in-flight turn on a node.
type ClusterJob struct {
	SessionID     string
	Title         string
	WorkspaceRoot string
	Color         string
	Animal        string
	Blocked       bool
}

// ClusterParams is the JSON schema exposed to the model.
type ClusterParams struct {
	Action string `json:"action" description:"'nodes' to list cluster members with health and running jobs, or 'spawn' to start a new session on a node"`
	Node   string `json:"node,omitempty" description:"With action='spawn': target node name, node id, or id prefix (from action='nodes'). 'auto' picks the online node with the most spare capacity."`
	Path   string `json:"path,omitempty" description:"With action='spawn': absolute project directory ON THE TARGET NODE. It must already exist there."`
	Prompt string `json:"prompt,omitempty" description:"With action='spawn': the initial task for the new session."`
	Title  string `json:"title,omitempty" description:"With action='spawn': optional session title."`
	Model  string `json:"model,omitempty" description:"With action='spawn': optional model (role name, provider/model, or bare id), resolved in the target node's config."`
}

// NewClusterTool builds the cluster tool. It is wrapped in a context
// gate so it is only advertised while the server is in a cluster.
func NewClusterTool(be SwarmBackend, cb ClusterBackend, sessions session.Service, swarmCfg func() swarm.Config, senderWorkspaceID string) fantasy.AgentTool {
	tool := fantasy.NewParallelAgentTool(
		ClusterToolName,
		clusterToolDescription,
		func(ctx context.Context, params ClusterParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if !cb.ClusterActive() {
				return fantasy.NewTextErrorResponse("cluster: this server is not part of a cluster"), nil
			}
			switch strings.ToLower(strings.TrimSpace(params.Action)) {
			case "nodes", "":
				return runClusterNodes(ctx, cb)
			case "spawn":
				return runClusterSpawn(ctx, be, cb, sessions, swarmCfg, senderWorkspaceID, params)
			default:
				return fantasy.NewTextErrorResponse(fmt.Sprintf("cluster: unknown action %q (want 'nodes' or 'spawn')", params.Action)), nil
			}
		},
	)
	return WithContextGate(tool, func(context.Context) bool { return cb.ClusterActive() })
}

func runClusterNodes(ctx context.Context, cb ClusterBackend) (fantasy.ToolResponse, error) {
	nodes, err := cb.ClusterNodes(ctx)
	if err != nil {
		if isContextErr(err) {
			return fantasy.ToolResponse{}, err
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("cluster: failed to list nodes: %s", err)), nil
	}
	return fantasy.NewTextResponse(formatClusterNodes(nodes)), nil
}

func formatClusterNodes(nodes []ClusterNode) string {
	nodes = slices.Clone(nodes)
	slices.SortFunc(nodes, func(a, b ClusterNode) int {
		if a.Self != b.Self {
			if a.Self {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d cluster node(s):\n", len(nodes))
	for _, n := range nodes {
		status := "offline"
		if n.Online {
			status = "online"
		}
		if n.Self {
			status += ", this node"
		}
		fmt.Fprintf(&sb, "\n- %s (id %s, %s)", n.Name, shortNodeID(n.NodeID), status)
		if n.HasStats {
			fmt.Fprintf(&sb, "\n  %s %s, cpu %.0f%% of %d cores, mem %.0f%%, %d sessions, free %.2f",
				n.Hostname, n.Platform, n.CPUPercent, n.CPUCores, n.MemPercent, n.Sessions, n.FreeScore)
		}
		if n.RTTMillis >= 0 && !n.Self {
			fmt.Fprintf(&sb, ", rtt %dms", n.RTTMillis)
		}
		for _, j := range n.Jobs {
			addr := swarm.FormatAddress(swarm.Identity{Color: j.Color, Animal: j.Animal}, j.SessionID)
			blocked := ""
			if j.Blocked {
				blocked = " [waiting on permission]"
			}
			fmt.Fprintf(&sb, "\n  running: %s %q in %s%s", addr, j.Title, j.WorkspaceRoot, blocked)
		}
	}
	return sb.String()
}

func shortNodeID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// pickAutoNode returns the online node with the highest free score among
// those reporting stats, preferring other nodes over this one on ties.
func pickAutoNode(nodes []ClusterNode) (ClusterNode, bool) {
	var best ClusterNode
	found := false
	for _, n := range nodes {
		if !n.Online || !n.HasStats {
			continue
		}
		switch {
		case !found, n.FreeScore > best.FreeScore:
			best, found = n, true
		case n.FreeScore == best.FreeScore && best.Self && !n.Self:
			best = n
		}
	}
	return best, found
}

func runClusterSpawn(
	ctx context.Context,
	be SwarmBackend,
	cb ClusterBackend,
	sessions session.Service,
	swarmCfg func() swarm.Config,
	senderWorkspaceID string,
	params ClusterParams,
) (fantasy.ToolResponse, error) {
	node := strings.TrimSpace(params.Node)
	path := strings.TrimSpace(params.Path)
	if node == "" {
		return fantasy.NewTextErrorResponse("cluster: spawn requires 'node' (see action='nodes', or use 'auto')"), nil
	}
	if path == "" {
		return fantasy.NewTextErrorResponse("cluster: spawn requires 'path' (a project directory on the target node)"), nil
	}
	if strings.TrimSpace(params.Prompt) == "" {
		return fantasy.NewTextErrorResponse("cluster: spawn requires 'prompt'"), nil
	}

	sender, errMsg, err := resolveSwarmSender(ctx, sessions, swarmCfg, senderWorkspaceID)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if errMsg != "" {
		return fantasy.NewTextErrorResponse("cluster: " + errMsg), nil
	}

	nodeLabel := node
	if strings.EqualFold(node, "auto") {
		nodes, err := cb.ClusterNodes(ctx)
		if err != nil {
			if isContextErr(err) {
				return fantasy.ToolResponse{}, err
			}
			return fantasy.NewTextErrorResponse(fmt.Sprintf("cluster: failed to list nodes: %s", err)), nil
		}
		best, ok := pickAutoNode(nodes)
		if !ok {
			return fantasy.NewTextErrorResponse("cluster: no online node with health stats available"), nil
		}
		node, nodeLabel = best.NodeID, best.Name
	}

	title := strings.TrimSpace(params.Title)
	if title == "" {
		title = firstLine(params.Prompt, 60)
	}
	target, err := cb.CreateSessionOnNode(ctx, node, path, title, strings.TrimSpace(params.Model))
	if err != nil {
		if isContextErr(err) {
			return fantasy.ToolResponse{}, err
		}
		return fantasy.NewTextErrorResponse(fmt.Sprintf("cluster: failed to create session on %s: %s", nodeLabel, err)), nil
	}
	addr := swarm.FormatAddress(swarm.Identity{Color: target.Color, Animal: target.Animal}, target.SessionID)
	delivery, hardErr, softErr := sendSwarmPart(ctx, be, sender, params.Prompt, false, target)
	if hardErr != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("cluster: created %s on %s but sending the prompt was interrupted: %w", addr, nodeLabel, hardErr)
	}
	if softErr != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf(
			"cluster: created %s on %s but failed to send the prompt: %s. Retry with swarm(address=%q).",
			addr, nodeLabel, softErr, addr,
		)), nil
	}
	return fantasy.NewTextResponse(fmt.Sprintf(
		"Created and %s to %s on node %s (session=%s, workspace=%s). It reports back to you (%s) only if your prompt asks it to reply via swarm.",
		delivery, addr, nodeLabel, target.SessionID, target.WorkspaceID, sender.address,
	)), nil
}
