package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/proto"
)

// activeSessionCount reports how many active (non-archived) sessions the
// local node lists across known workspaces.
func activeSessionCount(t *testing.T, h http.Handler) int {
	t.Helper()
	var overviews proto.DeviceSessionOverviewPage
	require.Equal(t, http.StatusOK, doJSON(t, h, http.MethodGet, "/v1/session-overviews?local=1", nil, &overviews))
	return overviews.Total
}

func archivedPath(root string) string {
	return "/v1/workspaces/-/sessions/archived?root=" + url.QueryEscape(root)
}

// TestArchivedSessionsJSONIsSnakeCase is a regression test: the archived
// list used to encode raw session.Session values (no json tags), so the
// client decoded zero id/title/updated_at.
func TestArchivedSessionsJSONIsSnakeCase(t *testing.T) {
	h, root := newSessionRoutesServer(t)
	require.Equal(t, http.StatusOK, doJSON(t, h, http.MethodPost, "/v1/workspaces/-/sessions/s1/archive?root="+url.QueryEscape(root), nil, nil))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, archivedPath(root), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	require.Len(t, raw, 1)
	for _, key := range []string{"id", "title", "updated_at", "archived_at"} {
		require.Contains(t, raw[0], key)
	}
	for _, key := range []string{"ID", "Title", "UpdatedAt", "ArchivedAt"} {
		require.NotContains(t, raw[0], key)
	}

	var sessions []proto.Session
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sessions))
	require.Equal(t, "s1", sessions[0].ID)
	require.Equal(t, "Detached", sessions[0].Title)
	require.NotZero(t, sessions[0].UpdatedAt)
	require.NotZero(t, sessions[0].ArchivedAt)
}

// TestClientListAndUnarchiveRoundTrip drives the client package the TUI
// uses: list archived sessions, unarchive one, and verify it moves back
// to the active list.
func TestClientListAndUnarchiveRoundTrip(t *testing.T) {
	h, root := newSessionRoutesServer(t)
	hs := httptest.NewServer(h)
	t.Cleanup(hs.Close)
	c, err := client.NewClient("", "tcp", hs.Listener.Addr().String())
	require.NoError(t, err)
	ctx := t.Context()

	require.Equal(t, 1, activeSessionCount(t, h))
	require.NoError(t, c.ArchiveSession(ctx, "", root, "s1"))
	require.Equal(t, 0, activeSessionCount(t, h))

	archived, err := c.ListArchivedSessions(ctx, "", root)
	require.NoError(t, err)
	require.Len(t, archived, 1)
	require.Equal(t, "s1", archived[0].ID)
	require.NotZero(t, archived[0].UpdatedAt)

	require.NoError(t, c.UnarchiveSession(ctx, "", root, archived[0].ID))
	archived, err = c.ListArchivedSessions(ctx, "", root)
	require.NoError(t, err)
	require.Empty(t, archived)
	require.Equal(t, 1, activeSessionCount(t, h))
}

func TestSessionBatchUnarchive(t *testing.T) {
	h, root := newSessionRoutesServer(t)
	targets := []proto.SessionBatchTarget{{Root: root, SessionID: "s1"}}

	var res proto.SessionBatchResult
	require.Equal(t, http.StatusOK, doJSON(t, h, http.MethodPost, "/v1/sessions/batch",
		proto.SessionBatchRequest{Op: proto.SessionBatchArchive, Targets: targets}, &res))
	require.Empty(t, res.Failed)
	require.Equal(t, 0, activeSessionCount(t, h))

	require.Equal(t, http.StatusOK, doJSON(t, h, http.MethodPost, "/v1/sessions/batch",
		proto.SessionBatchRequest{Op: proto.SessionBatchUnarchive, Targets: targets}, &res))
	require.Empty(t, res.Failed)
	require.Equal(t, 1, activeSessionCount(t, h))

	var archived []proto.Session
	require.Equal(t, http.StatusOK, doJSON(t, h, http.MethodGet, archivedPath(root), nil, &archived))
	require.Empty(t, archived)
}
