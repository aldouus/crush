package cluster

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeviceConnsCounting(t *testing.T) {
	t.Parallel()
	d := newDeviceConns(0)
	now := time.Now()
	n, evict := d.add("UA", deviceConn{cid: 1, start: now})
	require.Equal(t, 1, n)
	require.Empty(t, evict)
	n, _ = d.add("UA", deviceConn{cid: 2, start: now.Add(time.Second)})
	require.Equal(t, 2, n)
	n, _ = d.add("UA", deviceConn{cid: 2, start: now.Add(time.Second)})
	require.Equal(t, 2, n, "duplicate connect events are idempotent")
	n, _ = d.add("UB", deviceConn{cid: 3, start: now})
	require.Equal(t, 1, n)
	for i := uint64(4); i < 10; i++ {
		_, evict = d.add("UA", deviceConn{cid: i, start: now.Add(time.Duration(i) * time.Second)})
		require.Empty(t, evict, "unlimited never evicts")
	}
	require.Equal(t, 8, d.count("UA"))
	require.Equal(t, 7, d.remove("UA", 1))
	require.Equal(t, 0, d.remove("UB", 3))
	require.Equal(t, 0, d.count("UB"))
	require.Equal(t, 0, d.remove("UC", 42))
}

func TestDeviceConnsEvictOldest(t *testing.T) {
	t.Parallel()
	d := newDeviceConns(2)
	now := time.Now()
	d.add("UA", deviceConn{cid: 10, start: now.Add(2 * time.Second)})
	d.add("UA", deviceConn{cid: 11, start: now})
	n, evict := d.add("UA", deviceConn{cid: 12, start: now.Add(3 * time.Second)})
	require.Equal(t, 3, n)
	require.Len(t, evict, 1)
	require.Equal(t, uint64(11), evict[0].cid, "the oldest by start time goes")

	d.setMax(1)
	_, evict = d.add("UA", deviceConn{cid: 13, start: now.Add(4 * time.Second)})
	require.Equal(t, []uint64{11, 10, 12}, cids(evict))
}

func TestMeshTrackConnectEvicts(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var closed []uint64
	m := &Mesh{conns: newDeviceConns(DefaultDeviceMaxConns), evict: func(cid uint64) error {
		mu.Lock()
		defer mu.Unlock()
		closed = append(closed, cid)
		return nil
	}}
	now := time.Now()
	for i := range uint64(3) {
		m.trackConnect("UA", deviceConn{cid: i + 1, remote: "10.0.0.1", start: now.Add(time.Duration(i) * time.Second)}, nil)
	}
	require.Equal(t, []uint64{1}, closed)
	m.conns.remove("UA", 1)
	require.Equal(t, 2, m.DeviceConns("UA"))

	m.SetDeviceMaxConns(0)
	m.trackConnect("UA", deviceConn{cid: 9, start: now.Add(time.Minute)}, nil)
	require.Equal(t, []uint64{1}, closed)
	require.Equal(t, 3, m.DeviceConns("UA"))
}

func cids(cs []deviceConn) []uint64 {
	out := make([]uint64, len(cs))
	for i, c := range cs {
		out[i] = c.cid
	}
	return out
}
