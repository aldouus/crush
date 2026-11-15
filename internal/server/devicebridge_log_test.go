package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeviceBridgeRequestLog(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	b := &deviceBridge{nodeID: "node-a", logger: slog.New(slog.NewJSONHandler(&buf, nil))}
	req := bridgeRequest{ID: "r1", Method: http.MethodGet, Path: "/v1/workspaces", Body: "secret-body"}

	b.logRequest("dev-1", req, bridgeResponse{ID: "r1", Status: http.StatusOK, NodeID: "node-a"}, time.Now())
	b.logRequest("dev-1", req, errorResponse("r1", "node-b", rateLimited()), time.Now())

	var lines []map[string]any
	for _, l := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var rec map[string]any
		require.NoError(t, json.Unmarshal(l, &rec))
		lines = append(lines, rec)
	}
	require.Len(t, lines, 2)
	require.Equal(t, "INFO", lines[0]["level"])
	require.Equal(t, "Device bridge request", lines[0]["msg"])
	require.Equal(t, "dev-1", lines[0]["device_id"])
	require.Equal(t, "node-a", lines[0]["node"])
	require.Equal(t, "GET", lines[0]["method"])
	require.Equal(t, "/v1/workspaces", lines[0]["path"])
	require.EqualValues(t, 200, lines[0]["status"])
	require.Contains(t, lines[0], "latency")

	require.Equal(t, "WARN", lines[1]["level"])
	require.Equal(t, "Device bridge request failed", lines[1]["msg"])
	require.Equal(t, string(codeRateLimited), lines[1]["error_code"])
	require.Equal(t, "node-b", lines[1]["served_by"])
	require.NotContains(t, buf.String(), "secret-body")
}
