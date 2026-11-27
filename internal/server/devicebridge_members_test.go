package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/cluster"
)

func TestDeviceBridgePublishesMembersOnChange(t *testing.T) {
	isolateHome(t)
	srv, c := initPhoneNode(t)
	ph := pairPhone(t, srv, c)
	require.Eventually(t, func() bool { return srv.pairing.bridge.Load() != nil }, deviceWait, 20*time.Millisecond)
	// The device's NATS user may still be installing right after pairing.
	var nc *nats.Conn
	require.Eventually(t, func() bool {
		var err error
		nc, err = ph.connect(t)
		return err == nil
	}, deviceWait, 50*time.Millisecond)
	t.Cleanup(nc.Close)
	notices := make(chan membersNotice, 8)
	_, err := nc.Subscribe(subjClientRoot+ph.id+"."+verbMembers, func(m *nats.Msg) {
		var n membersNotice
		if json.Unmarshal(m.Data, &n) == nil {
			notices <- n
		}
	})
	require.NoError(t, err)
	require.NoError(t, nc.Flush())

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	newID := "node-learned-late"
	require.NoError(t, srv.pairing.node.Store().Update(func(st *cluster.State) error {
		st.Members = append(st.Members, cluster.Member{NodeID: newID, Name: "mini3", PublicKey: pub, JoinedAt: time.Now().UTC()})
		return nil
	}))

	deadline := time.After(deviceWait)
	for {
		select {
		case n := <-notices:
			require.Equal(t, wireVersion, n.V)
			require.Equal(t, verbMembers, n.Type)
			var ids []string
			for _, m := range n.Members {
				ids = append(ids, m.NodeID)
			}
			if len(ids) == 2 {
				require.ElementsMatch(t, []string{ph.nodeID, newID}, ids)
				return
			}
		case <-deadline:
			t.Fatal("no members notice after a membership change")
		}
	}
}
