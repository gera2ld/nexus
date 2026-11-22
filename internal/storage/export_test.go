package storage

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"github.com/rclone/rclone/fs/config/configmap"
)

// Config exposes the merged rclone config for tests.
func (m *Manager) Config(acc *core.Record) (configmap.Simple, error) { return m.config(acc) }

// Cached reports how many mounts are open.
func (m *Manager) Cached() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.cache)
}
