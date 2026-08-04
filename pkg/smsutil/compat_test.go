package smsutil

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/swordstudiox/vohive-plus/pkg/smscodec"
	"github.com/warthog618/sms/encoding/tpdu"
)

var (
	_ RPDUKind            = smscodec.RPDUKindData
	_ RPDUInfo            = smscodec.RPDUInfo{}
	_ ConcatInfo          = smscodec.ConcatInfo{}
	_ OmaCPCharacteristic = smscodec.OmaCPCharacteristic{}
	_ OmaCPConfig         = smscodec.OmaCPConfig{}
)

func TestCompatShimLayoutAndFacade(t *testing.T) {
	t.Helper()

	if _, err := os.Stat(filepath.Join(".", "compat.go")); err != nil {
		t.Fatalf("compat.go must exist: %v", err)
	}

	legacyFiles := []string{
		"pdu.go",
		"pdu_trim.go",
		"wbxml_omacp.go",
		"binary_classifier.go",
		"pdu_rp_test.go",
		"pdu_trim_test.go",
		"binary_classifier_test.go",
		"wbxml_omacp_test.go",
	}
	for _, name := range legacyFiles {
		if _, err := os.Stat(filepath.Join(".", name)); err == nil {
			t.Fatalf("legacy file %s must be removed", name)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", name, err)
		}
	}

	if RPDUKindData != smscodec.RPDUKindData {
		t.Fatalf("RPDUKindData mismatch: got %q want %q", RPDUKindData, smscodec.RPDUKindData)
	}
	if RPCauseTemporaryFailure != smscodec.RPCauseTemporaryFailure {
		t.Fatalf("RPCauseTemporaryFailure mismatch: got %d want %d", RPCauseTemporaryFailure, smscodec.RPCauseTemporaryFailure)
	}

	udh := tpdu.UserDataHeader{{ID: 0x05, Data: []byte{0x0B, 0x84, 0x23, 0xF0}}}
	if !IsOmaCPMessage(udh) {
		t.Fatal("IsOmaCPMessage() = false, want true")
	}
	if !IsHexString("0A0B") {
		t.Fatal("IsHexString() = false, want true")
	}
	if got := BuildRPAck(0x22); !bytes.Equal(got, smscodec.BuildRPAck(0x22)) {
		t.Fatalf("BuildRPAck() mismatch: got %x want %x", got, smscodec.BuildRPAck(0x22))
	}
}
