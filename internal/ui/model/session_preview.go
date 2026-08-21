package model

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/util"
)

const (
	previewDebounce     = 150 * time.Millisecond
	previewBurstWindow  = 250 * time.Millisecond
	previewBurstInstant = 2
)

type previewTickMsg struct{ gen int }

type previewRequest struct {
	key   previewKey
	local bool
	gen   int
}

type previewLoadedMsg struct {
	request previewRequest
	msgs    []message.Message
	cached  bool
}

type previewLoadFailedMsg struct {
	request previewRequest
	err     error
}

func (m *UI) schedulePreview(id, root string) tea.Cmd {
	root = m.normalizePreviewRoot(root)
	if id == "" || m.isCommittedSession(id) {
		return m.cancelPreview()
	}
	if id == m.pendingPreviewID && root == m.pendingPreviewRoot {
		return nil
	}
	if id == m.previewSessionID && root == m.previewSessionRoot {
		m.clearPendingPreview()
		m.previewGen++
		return nil
	}

	m.pendingPreviewID = id
	m.pendingPreviewRoot = root
	m.previewGen++
	gen := m.previewGen
	request := m.previewRequest(id, root, gen)
	if msgs, ok := m.previewCache.get(request.key, m.previewTime()); ok {
		return func() tea.Msg {
			return previewLoadedMsg{request: request, msgs: msgs, cached: true}
		}
	}
	if m.registerPreviewBurst() {
		return m.previewLoadCmd(request)
	}
	return tea.Tick(previewDebounce, func(time.Time) tea.Msg {
		return previewTickMsg{gen: gen}
	})
}

func (m *UI) normalizePreviewRoot(root string) string {
	if root != "" && m.isCurrentWorkspace(root) {
		return ""
	}
	return root
}

func (m *UI) isCommittedSession(id string) bool {
	return m.session != nil && m.session.ID == id
}

func (m *UI) previewRequest(id, root string, gen int) previewRequest {
	request := previewRequest{local: root == "", gen: gen}
	if request.local {
		root = m.com.Workspace.BaseDir()
	}
	request.key = previewKey{sessionID: id, root: root}
	return request
}

func (m *UI) previewTime() time.Time {
	if m.previewNow != nil {
		return m.previewNow()
	}
	return time.Now()
}

func (m *UI) clearPendingPreview() {
	m.pendingPreviewID = ""
	m.pendingPreviewRoot = ""
}

func (m *UI) resetPreview() {
	m.previewSessionID = ""
	m.previewSessionRoot = ""
	m.clearPendingPreview()
	m.previewGen++
}

func (m *UI) registerPreviewBurst() bool {
	now := m.previewTime()
	if m.previewBurstCount == 0 || now.Sub(m.previewLastNav) > previewBurstWindow {
		m.previewBurstCount = 0
	}
	m.previewLastNav = now
	m.previewBurstCount++
	return m.previewBurstCount <= previewBurstInstant
}

func (m *UI) handlePreviewTick(msg previewTickMsg) tea.Cmd {
	if msg.gen != m.previewGen || m.pendingPreviewID == "" {
		return nil
	}
	request := m.previewRequest(m.pendingPreviewID, m.pendingPreviewRoot, msg.gen)
	return m.previewLoadCmd(request)
}

func (m *UI) previewLoadCmd(request previewRequest) tea.Cmd {
	return func() tea.Msg {
		msgs, err := m.com.Workspace.PeekMessages(context.Background(), request.key.root, request.key.sessionID)
		if err != nil {
			return previewLoadFailedMsg{request: request, err: err}
		}
		return previewLoadedMsg{request: request, msgs: msgs}
	}
}

func (m *UI) handlePreviewLoadFailed(msg previewLoadFailedMsg) tea.Cmd {
	if !m.isCurrentPreviewRequest(msg.request) {
		return nil
	}
	m.clearPendingPreview()
	return util.ReportError(msg.err)
}

func (m *UI) handlePreviewLoaded(msg previewLoadedMsg) tea.Cmd {
	if !m.isCurrentPreviewRequest(msg.request) {
		return nil
	}
	m.previewSessionID = msg.request.key.sessionID
	m.previewSessionRoot = m.pendingPreviewRoot
	m.clearPendingPreview()
	if !msg.cached {
		m.previewCache.put(msg.request.key, msg.msgs, m.previewTime())
	}
	return m.setSessionMessages(msg.msgs)
}

func (m *UI) isCurrentPreviewRequest(request previewRequest) bool {
	if request.key.sessionID != m.pendingPreviewID || request.gen != m.previewGen {
		return false
	}
	return request.local == (m.pendingPreviewRoot == "") &&
		(request.local || request.key.root == m.pendingPreviewRoot)
}

func (m *UI) cancelPreview() tea.Cmd {
	m.clearPendingPreview()
	if m.previewSessionID == "" {
		return nil
	}
	m.previewSessionID = ""
	m.previewSessionRoot = ""
	m.previewGen++
	if m.session == nil {
		m.chat.ClearMessages()
		return nil
	}
	return m.reloadCommittedMessages(m.previewGen)
}

func (m *UI) reloadCommittedMessages(gen int) tea.Cmd {
	if m.session == nil {
		return nil
	}
	id := m.session.ID
	return func() tea.Msg {
		msgs, err := m.com.Workspace.ListMessages(context.Background(), id)
		if err != nil {
			return util.ReportError(err)()
		}
		return previewRestoreMsg{id: id, msgs: msgs, gen: gen}
	}
}

type previewRestoreMsg struct {
	id   string
	msgs []message.Message
	gen  int
}

func (m *UI) handlePreviewRestore(msg previewRestoreMsg) tea.Cmd {
	if msg.gen != m.previewGen || m.previewSessionID != "" || m.pendingPreviewID != "" {
		return nil
	}
	if m.session == nil || m.session.ID != msg.id {
		return nil
	}
	return m.setSessionMessages(msg.msgs)
}

func (m *UI) previewing() bool { return m.previewSessionID != "" }
