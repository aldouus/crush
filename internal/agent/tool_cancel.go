package agent

import (
	"context"
	"errors"
	"sync"

	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/fantasy"
)

// errToolCanceledByUser is the cancellation cause recorded when the user
// cancels the in-flight tool calls of a turn without canceling the turn.
var errToolCanceledByUser = errors.New("tool call canceled by the user")

// toolCanceledResponse is what the model sees in place of a tool result
// the user canceled. The turn keeps going, so it is phrased to steer the
// model rather than to report a failure.
const toolCanceledResponse = "The user canceled this tool call before it finished. Do not retry it unless they ask you to; continue with the rest of the task, or check in with the user if you are blocked."

// toolCancelRegistry tracks the cancel funcs of tool calls currently
// executing, per session, so they can be canceled without canceling the
// turn that issued them. The zero value is ready to use.
type toolCancelRegistry struct {
	mu        sync.Mutex
	nextID    uint64
	bySession map[string]*sessionToolCalls
	// bgPending marks sessions with an outstanding background request so
	// tool calls that start before the request is satisfied are signaled
	// on registration.
	bgPending map[string]bool
}

type sessionToolCalls struct {
	cancels     map[uint64]context.CancelCauseFunc
	backgrounds map[uint64]chan struct{}
}

// register records cancel for a running tool call in sessionID and returns
// a func that removes it once the call returns. bg, if non-nil, is closed
// by requestBackground.
func (r *toolCancelRegistry) register(sessionID string, cancel context.CancelCauseFunc, bg chan struct{}) func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bySession == nil {
		r.bySession = make(map[string]*sessionToolCalls)
	}
	calls := r.bySession[sessionID]
	if calls == nil {
		calls = &sessionToolCalls{
			cancels:     make(map[uint64]context.CancelCauseFunc),
			backgrounds: make(map[uint64]chan struct{}),
		}
		r.bySession[sessionID] = calls
	}
	r.nextID++
	id := r.nextID
	calls.cancels[id] = cancel
	if bg != nil {
		if r.bgPending[sessionID] {
			close(bg)
		} else {
			calls.backgrounds[id] = bg
		}
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(calls.cancels, id)
		delete(calls.backgrounds, id)
		if len(calls.cancels) == 0 && r.bySession[sessionID] == calls {
			delete(r.bySession, sessionID)
		}
	}
}

// cancel cancels every tool call running in sessionID and reports how
// many there were.
func (r *toolCancelRegistry) cancel(sessionID string) int {
	r.mu.Lock()
	var cancels []context.CancelCauseFunc
	if calls := r.bySession[sessionID]; calls != nil {
		for _, cancel := range calls.cancels {
			cancels = append(cancels, cancel)
		}
	}
	r.mu.Unlock()
	for _, cancel := range cancels {
		cancel(errToolCanceledByUser)
	}
	return len(cancels)
}

// requestBackground asks every tool call running in sessionID to move
// itself to the background (tools that support it do so once they have
// run for tools.BackgroundOnRequestAfter). It reports how many calls were
// signaled. The request stays pending, signaling calls that start later,
// until clearBackgroundRequest.
func (r *toolCancelRegistry) requestBackground(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bgPending == nil {
		r.bgPending = make(map[string]bool)
	}
	r.bgPending[sessionID] = true
	calls := r.bySession[sessionID]
	if calls == nil {
		return 0
	}
	n := len(calls.backgrounds)
	for id, bg := range calls.backgrounds {
		close(bg)
		delete(calls.backgrounds, id)
	}
	return n
}

// clearBackgroundRequest drops the pending background request for
// sessionID, once the aside that asked for it has been folded in.
func (r *toolCancelRegistry) clearBackgroundRequest(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.bgPending, sessionID)
}

// activeTurn describes the turn currently streaming in a session, for
// decisions that depend on it (see PromoteNextQueuedToBTW).
type activeTurn struct {
	runID  string
	policy permission.SessionPolicy
}

// activeTurnRegistry records the active turn per session. The zero value
// is ready to use.
type activeTurnRegistry struct {
	mu    sync.Mutex
	turns map[string]activeTurn
}

func (r *activeTurnRegistry) set(sessionID string, turn activeTurn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turns == nil {
		r.turns = make(map[string]activeTurn)
	}
	r.turns[sessionID] = turn
}

func (r *activeTurnRegistry) get(sessionID string) (activeTurn, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	turn, ok := r.turns[sessionID]
	return turn, ok
}

func (r *activeTurnRegistry) clear(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.turns, sessionID)
}

// CancelToolCalls cancels the tool calls currently executing in sessionID
// without canceling its turn or touching its queued prompts. Each canceled
// call returns a "canceled by the user" result to the model, so the turn
// continues at its next step (where queued /btw asides are folded in). It
// reports how many tool calls were canceled.
func (a *sessionAgent) CancelToolCalls(sessionID string) int {
	return a.toolCancels.cancel(sessionID)
}

// wrapToolsWithUserCancel makes each tool's execution individually
// cancelable through registry for sessionID.
func wrapToolsWithUserCancel(agentTools []fantasy.AgentTool, registry *toolCancelRegistry, sessionID string) []fantasy.AgentTool {
	if registry == nil || len(agentTools) == 0 {
		return agentTools
	}
	wrapped := make([]fantasy.AgentTool, len(agentTools))
	for i, tool := range agentTools {
		wrapped[i] = &userCancelableTool{AgentTool: tool, registry: registry, sessionID: sessionID}
	}
	return wrapped
}

type userCancelableTool struct {
	fantasy.AgentTool
	registry  *toolCancelRegistry
	sessionID string
}

func (t *userCancelableTool) Run(parentCtx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	ctx, cancel := context.WithCancelCause(parentCtx)
	defer cancel(nil)
	bg := make(chan struct{})
	ctx = tools.WithBackgroundRequest(ctx, bg)
	unregister := t.registry.register(t.sessionID, cancel, bg)
	defer unregister()

	resp, err := t.AgentTool.Run(ctx, call)
	if !errors.Is(context.Cause(ctx), errToolCanceledByUser) || parentCtx.Err() != nil {
		// Not a user tool-cancel, or the whole turn is being canceled as
		// well: return the outcome unchanged so a turn cancel still ends
		// the turn.
		return resp, err
	}
	if err == nil && !resp.IsError {
		// The tool finished on its own before (or despite) the cancel.
		// Keep its real result: its side effects already happened, and
		// telling the model it was canceled would mislead it.
		return resp, nil
	}
	return fantasy.NewTextErrorResponse(toolCanceledResponse), nil
}
