package mbimcore

import (
	"context"
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

// NewForTest creates an opened Manager backed by the given transport, for
// cross-package tests that need a *Manager without real hardware.
func NewForTest(tr mbim.Transport) (*Manager, error) {
	m := New("/dev/cdc-wdm0-test", "direct")
	m.healthProbeInterval = 30 * time.Millisecond
	m.healthProbeTimeout = 20 * time.Millisecond
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		return nil, err
	}
	return m, nil
}
