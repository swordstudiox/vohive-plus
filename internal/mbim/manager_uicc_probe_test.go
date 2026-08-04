package mbimcore

import (
	"errors"
	"testing"

	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

// 新固件下 OPEN_CHANNEL 对某个 AID 返回 MS UICC 专有状态码(SelectFailed 等),
// 证明服务本身已支持,只是该 AID 选择失败 —— 应判为"支持"。只有 NoDeviceSupport
// 或普通错误才判"不支持"。
func TestUICCSupportedFromOpenErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"select_failed", &mbim.StatusError{Op: "UICC_OPEN_CHANNEL", Status: mbim.StatusMSSelectFailed}, true},
		{"no_logical_channels", &mbim.StatusError{Status: mbim.StatusMSNoLogicalChannels}, true},
		{"invalid_logical_channel", &mbim.StatusError{Status: mbim.StatusMSInvalidLogicalChannel}, true},
		{"no_device_support", &mbim.StatusError{Op: "UICC_OPEN_CHANNEL", Status: 0x9}, false},
		{"generic_error", errors.New("device not opened"), false},
	}
	for _, c := range cases {
		if got := uiccSupportedFromOpenErr(c.err); got != c.want {
			t.Fatalf("%s: uiccSupportedFromOpenErr = %v, want %v", c.name, got, c.want)
		}
	}
}
