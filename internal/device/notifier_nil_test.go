package device

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

type testNotifier struct {
	ipRotated atomic.Bool
}

func (n *testNotifier) NotifySMS(deviceID, sender, content string, timestamp time.Time) {}

func (n *testNotifier) NotifyIPRotated(deviceID, oldIP, newIP string, duration time.Duration) {
	n.ipRotated.Store(true)
}

func (n *testNotifier) NotifyRaw(msg string) {}

func TestNormalizeNotifierTypedNil(t *testing.T) {
	var typedNil *testNotifier
	if got := normalizeNotifier(typedNil); got != nil {
		t.Fatalf("expected nil notifier after normalize, got %T", got)
	}
}

func TestNotifyIPChangedIgnoresTypedNilNotifier(t *testing.T) {
	p := NewPool(&config.Config{})

	var typedNil *testNotifier
	p.SetNotifier(typedNil)

	if got := p.getNotifier(); got != nil {
		t.Fatalf("expected pool notifier to be nil after SetNotifier(typed nil), got %T", got)
	}

	p.NotifyIPChanged("wwan0", "1.1.1.1", "2.2.2.2", 20*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
}

func TestNotifyIPChangedCallsValidNotifier(t *testing.T) {
	p := NewPool(&config.Config{})
	n := &testNotifier{}
	p.SetNotifier(n)

	p.NotifyIPChanged("wwan0", "1.1.1.1", "2.2.2.2", 20*time.Millisecond)

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if n.ipRotated.Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected notifier.NotifyIPRotated to be called")
}
