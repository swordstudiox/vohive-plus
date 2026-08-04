package mbimcore

import (
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/apduarbiter"
)

func TestManagerSetAPDUArbiterStores(t *testing.T) {
	m := New("/dev/cdc-wdm0", "auto")
	arb := apduarbiter.New("dev", apduarbiter.Options{MaxSessions: 3, MaxQMITransports: 3})
	m.SetAPDUArbiter(arb)
	if m.apduArbiter != arb {
		t.Fatal("SetAPDUArbiter 应保存 arbiter 引用")
	}
}
