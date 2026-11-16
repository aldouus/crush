package cmd

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/projects"
	"github.com/taigrr/crush/internal/session"
)

func newArchiveTestService(t *testing.T) session.Service {
	t.Helper()
	dataDir := t.TempDir()
	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Release(dataDir) })
	return session.NewService(db.New(conn), conn)
}

func TestArchiveSessionsRoundTrip(t *testing.T) {
	ctx := t.Context()
	svc := newArchiveTestService(t)
	first, err := svc.Create(ctx, "first")
	require.NoError(t, err)
	second, err := svc.Create(ctx, "second")
	require.NoError(t, err)

	// Archive by hash prefix and full UUID.
	results, errs := archiveSessions(ctx, svc, []string{session.HashID(first.ID)[:10], second.ID}, true)
	require.Empty(t, errs)
	require.Len(t, results, 2)
	require.True(t, results[0].Archived)
	require.Equal(t, first.ID, results[0].UUID)

	active, err := svc.List(ctx)
	require.NoError(t, err)
	require.Empty(t, active)

	// Archiving again fails: archive resolves only active sessions.
	_, errs = archiveSessions(ctx, svc, []string{first.ID}, true)
	require.Len(t, errs, 1)
	require.ErrorContains(t, errs[0], "active session not found")

	// Unarchive resolves archived sessions by prefix.
	results, errs = archiveSessions(ctx, svc, []string{session.HashID(first.ID)[:10]}, false)
	require.Empty(t, errs)
	require.Len(t, results, 1)
	require.True(t, results[0].Unarchived)

	active, err = svc.List(ctx)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, first.ID, active[0].ID)
	archived, err := svc.ListArchived(ctx)
	require.NoError(t, err)
	require.Len(t, archived, 1)
	require.Equal(t, second.ID, archived[0].ID)

	// Unarchiving an active session fails; other refs still apply.
	results, errs = archiveSessions(ctx, svc, []string{first.ID, "nope", second.ID}, false)
	require.Len(t, errs, 2)
	require.ErrorContains(t, errs[0], "archived session not found")
	require.Len(t, results, 1)
	require.Equal(t, second.ID, results[0].UUID)
}

func TestResolveSessionIDFindsArchived(t *testing.T) {
	ctx := t.Context()
	svc := newArchiveTestService(t)
	sess, err := svc.Create(ctx, "archived one")
	require.NoError(t, err)
	require.NoError(t, svc.Archive(ctx, sess.ID))

	prefix := session.HashID(sess.ID)[:10]
	got, err := resolveSessionID(ctx, svc, prefix)
	require.NoError(t, err)
	require.Equal(t, sess.ID, got.ID)

	got, err = resolveSessionIDIn(ctx, svc, prefix, sessionScopeArchived)
	require.NoError(t, err)
	require.Equal(t, sess.ID, got.ID)

	_, err = resolveSessionIDIn(ctx, svc, prefix, sessionScopeActive)
	require.ErrorContains(t, err, "active session not found")
}

func TestSessionListArchivedJSON(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	project := filepath.Join(t.TempDir(), "proj")
	dataDir := filepath.Join(project, ".crush")
	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	svc := session.NewService(db.New(conn), conn)
	_, err = svc.Create(t.Context(), "still active")
	require.NoError(t, err)
	gone, err := svc.Create(t.Context(), "put away")
	require.NoError(t, err)
	require.NoError(t, svc.Archive(t.Context(), gone.ID))
	require.NoError(t, db.Release(dataDir))
	require.NoError(t, projects.Register(project, dataDir))

	sessionListAll = true
	sessionListJSON = true
	sessionListArchived = true
	t.Cleanup(func() {
		sessionListAll = false
		sessionListJSON = false
		sessionListArchived = false
	})

	var out, errOut bytes.Buffer
	sessionListCmd.SetOut(&out)
	sessionListCmd.SetErr(&errOut)
	sessionListCmd.SetContext(t.Context())
	require.NoError(t, runSessionList(sessionListCmd, nil))
	require.Empty(t, errOut.String())

	var got []sessionJSON
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got, 1)
	require.Equal(t, "put away", got[0].Title)
	require.Equal(t, gone.ID, got[0].UUID)
	require.NotEmpty(t, got[0].Archived)
}
