package device

import (
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestPoolNotifyDataConnectedDispatchesHandlers(t *testing.T) {
	p := NewPool(&config.Config{})
	got := make(chan string, 1)

	p.OnDataConnected(func(deviceID string) {
		got <- deviceID
	})

	p.notifyDataConnected("dev-proxy")

	select {
	case deviceID := <-got:
		if deviceID != "dev-proxy" {
			t.Fatalf("device id mismatch: got=%q want=dev-proxy", deviceID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for data-connected handler")
	}
}
