package cluster

import (
	"log/slog"
	"slices"
	"sync"
	"time"
)

// DefaultDeviceMaxConns is the default cap on concurrent NATS connections
// per paired device: one live connection plus one reconnecting overlap.
const DefaultDeviceMaxConns = 2

// deviceConn is one live device connection on this server.
type deviceConn struct {
	cid    uint64
	remote string
	start  time.Time
}

// deviceConns counts live connections per device nkey and, when capped,
// picks the oldest ones to evict so leaked connections cannot pile up.
type deviceConns struct {
	mu    sync.Mutex
	max   int
	conns map[string][]deviceConn
}

func newDeviceConns(limit int) *deviceConns {
	return &deviceConns{max: limit, conns: map[string][]deviceConn{}}
}

func (d *deviceConns) setMax(limit int) {
	d.mu.Lock()
	d.max = limit
	d.mu.Unlock()
}

// add records a connection and returns the live count (including it)
// and the connections to evict, oldest first. Evicted connections stay
// tracked until their disconnect is seen.
func (d *deviceConns) add(nkey string, c deviceConn) (int, []deviceConn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	list := d.conns[nkey]
	if slices.ContainsFunc(list, func(e deviceConn) bool { return e.cid == c.cid }) {
		return len(list), nil
	}
	list = append(list, c)
	slices.SortStableFunc(list, func(a, b deviceConn) int { return a.start.Compare(b.start) })
	d.conns[nkey] = list
	if d.max <= 0 || len(list) <= d.max {
		return len(list), nil
	}
	var evict []deviceConn
	for _, e := range list[:len(list)-d.max] {
		if e.cid != c.cid {
			evict = append(evict, e)
		}
	}
	return len(list), evict
}

// remove forgets a connection and returns the remaining count.
func (d *deviceConns) remove(nkey string, cid uint64) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	list := slices.DeleteFunc(d.conns[nkey], func(e deviceConn) bool { return e.cid == cid })
	if len(list) == 0 {
		delete(d.conns, nkey)
		return 0
	}
	d.conns[nkey] = list
	return len(list)
}

// count reports the live connections for nkey.
func (d *deviceConns) count(nkey string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.conns[nkey])
}

// SetDeviceMaxConns caps concurrent connections per device (0 =
// unlimited). New connections over the cap evict the oldest ones.
func (m *Mesh) SetDeviceMaxConns(limit int) {
	m.conns.setMax(max(0, limit))
}

// DeviceConns reports the live NATS connections for a device nkey.
func (m *Mesh) DeviceConns(nkey string) int { return m.conns.count(nkey) }

// trackConnect counts a device connection and evicts the oldest ones
// over the cap.
func (m *Mesh) trackConnect(nkey string, c deviceConn, args []any) {
	n, evict := m.conns.add(nkey, c)
	connArgs := append(slices.Clone(args), "conns", n)
	slog.Info("Device NATS auth ok", connArgs...)
	if n > 1 {
		slog.Warn("Device has multiple NATS connections", connArgs...)
	}
	for _, e := range evict {
		ev := append(slices.Clone(args), "evicted_cid", e.cid,
			"evicted_remote_addr", e.remote, "age", time.Since(e.start).Round(time.Millisecond))
		if err := m.disconnect(e.cid); err != nil {
			slog.Warn("Failed to evict device connection", append(ev, "error", err)...)
			continue
		}
		slog.Warn("Evicted oldest device connection", ev...)
	}
}

func (m *Mesh) disconnect(cid uint64) error {
	if m.evict != nil {
		return m.evict(cid)
	}
	m.mu.Lock()
	srv := m.srv
	m.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.DisconnectClientByID(cid)
}
