package cluster

import (
	"encoding/json"
	"fmt"
	"log/slog"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// eventsUser is the in-process system-account user that watches device
// connect, disconnect and auth error advisories.
const eventsUser = "crush-events"

// watchDeviceEvents subscribes to the system advisories for device
// connections on this server and logs them. Advisories are cluster-wide,
// so events from other servers are skipped.
func (m *Mesh) watchDeviceEvents(nc *nats.Conn, serverID string) error {
	logEvent := func(msg *nats.Msg) {
		var ev natsserver.DisconnectEventMsg
		if err := json.Unmarshal(msg.Data, &ev); err != nil {
			return
		}
		if ev.Server.ID != serverID {
			return
		}
		m.logDeviceEvent(msg.Subject, ev)
	}
	subjects := []string{
		fmt.Sprintf("$SYS.ACCOUNT.%s.CONNECT", ClientsAccount),
		fmt.Sprintf("$SYS.ACCOUNT.%s.DISCONNECT", ClientsAccount),
		fmt.Sprintf("$SYS.SERVER.%s.CLIENT.AUTH.ERR", serverID),
	}
	for _, s := range subjects {
		if _, err := nc.Subscribe(s, logEvent); err != nil {
			return fmt.Errorf("subscribe %s: %w", s, err)
		}
	}
	return nil
}

// logDeviceEvent logs one advisory. Only public identifiers are logged.
func (m *Mesh) logDeviceEvent(subject string, ev natsserver.DisconnectEventMsg) {
	c := ev.Client
	if c.User == meshUser || c.User == bridgeUser || c.User == eventsUser {
		return
	}
	args := []any{
		"node", m.node.id.Name,
		"nkey", c.User,
		"remote_addr", c.Host,
		"client_type", c.ClientType,
		"cid", c.ID,
	}
	if id := m.DeviceID(c.User); id != "" {
		args = append(args, "device_id", id)
	}
	if c.Name != "" {
		args = append(args, "client_name", c.Name)
	}
	switch ev.Type {
	case natsserver.ConnectEventMsgType:
		slog.Info("Device NATS auth ok", args...)
	case natsserver.DisconnectEventMsgType:
		args = append(args, "reason", ev.Reason,
			"msgs_in", ev.Received.Msgs, "msgs_out", ev.Sent.Msgs)
		if c.Start != nil && c.Stop != nil {
			args = append(args, "duration", c.Stop.Sub(*c.Start))
		}
		if ev.Server.ID != "" && subject == fmt.Sprintf("$SYS.SERVER.%s.CLIENT.AUTH.ERR", ev.Server.ID) {
			slog.Warn("Device NATS auth failed", args...)
			return
		}
		slog.Info("Device NATS disconnected", args...)
	}
}

// DeviceID returns the device registered for a public nkey ("" if none).
func (m *Mesh) DeviceID(nkey string) string {
	m.devMu.RLock()
	defer m.devMu.RUnlock()
	return m.devices[nkey]
}

func (m *Mesh) setDevices(users []ClientUser) {
	devices := make(map[string]string, len(users))
	for _, u := range users {
		if u.DeviceID != "" {
			devices[u.Nkey] = u.DeviceID
		}
	}
	m.devMu.Lock()
	m.devices = devices
	m.devMu.Unlock()
}

// slowConsumerHandler logs slow consumers on an in-process connection.
func slowConsumerHandler(node, conn string) nats.ErrHandler {
	return func(_ *nats.Conn, sub *nats.Subscription, err error) {
		args := []any{"node", node, "conn", conn, "error", err}
		if sub != nil {
			args = append(args, "subject", sub.Subject)
			if n, _, perr := sub.Pending(); perr == nil {
				args = append(args, "pending", n)
			}
		}
		if err == nats.ErrSlowConsumer {
			slog.Warn("NATS slow consumer", args...)
			return
		}
		slog.Warn("NATS connection error", args...)
	}
}
