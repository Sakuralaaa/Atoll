package agent

import "github.com/Sakuralaaa/Atoll/internal/api"

// queuedWhileOffline reports whether an agent->server event must survive a disconnect:
// deploy results and backup results are replayed on reconnect, everything else is dropped.
func queuedWhileOffline(msgType string) bool {
	return msgType == api.MsgDeploy || msgType == api.MsgBackupEvent
}

// Locked runs fn while holding the service's lock, serializing it with deploys, actions and
// syncs of the same service (used by backup restores).
func (m *Manager) Locked(serviceID string, fn func() error) error {
	defer m.lock(serviceID)()
	defer m.changed()
	return fn()
}
