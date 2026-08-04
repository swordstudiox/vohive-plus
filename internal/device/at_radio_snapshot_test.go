package device

import (
	"context"
	"errors"
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/modem"
)

type atRadioSnapshotTestQuerier struct {
	csqRSSI int
	csqDBM  int
	csqErr  error

	regStatus int
	regText   string
	regErr    error

	cell    modem.ServingCellLTEInfo
	cellErr error

	mode     string
	duplex   string
	band     string
	channel  uint32
	radioErr error

	operator    string
	operatorErr error
}

func (q *atRadioSnapshotTestQuerier) QueryCSQ() (int, int, error) {
	if q.csqErr != nil {
		return 0, -999, q.csqErr
	}
	return q.csqRSSI, q.csqDBM, nil
}

func (q *atRadioSnapshotTestQuerier) QueryRegistration() (int, string, string, string, error) {
	if q.regErr != nil {
		return 0, "", "", "", q.regErr
	}
	return q.regStatus, q.regText, "", "", nil
}

func (q *atRadioSnapshotTestQuerier) QueryServingCellLTEInfo() (modem.ServingCellLTEInfo, error) {
	return q.cell, q.cellErr
}

func (q *atRadioSnapshotTestQuerier) QueryNetworkRadio() (string, string, string, uint32, error) {
	return q.mode, q.duplex, q.band, q.channel, q.radioErr
}

func (q *atRadioSnapshotTestQuerier) QueryOperator() (string, error) {
	return q.operator, q.operatorErr
}

func TestReadATRadioSnapshotUsesEachAvailableField(t *testing.T) {
	q := &atRadioSnapshotTestQuerier{
		csqRSSI:   22,
		csqDBM:    -75,
		regStatus: 2,
		regText:   "搜索中",
		cell: modem.ServingCellLTEInfo{
			RSRP:    -104,
			RSRQ:    -8,
			SINR:    12,
			Duplex:  "FDD",
			Band:    "LTE BAND 8",
			Channel: 3740,
		},
		mode:     "LTE",
		duplex:   "FDD",
		band:     "LTE BAND 8",
		channel:  3740,
		operator: "CTExcelbiz",
	}

	s := ReadATRadioSnapshot(context.Background(), q, ATRadioReadOptions{Attempts: 1})

	if s.SignalDBM == nil || *s.SignalDBM != -75 {
		t.Fatalf("SignalDBM=%v want -75", s.SignalDBM)
	}
	if s.SignalRSRP == nil || *s.SignalRSRP != -104 || s.SignalRSRQ == nil || *s.SignalRSRQ != -8 || s.SignalSINR == nil || *s.SignalSINR != 12 {
		t.Fatalf("LTE signal fields missing: %+v", s)
	}
	if s.NetworkMode == nil || *s.NetworkMode != "LTE" || s.NetworkDuplex == nil || *s.NetworkDuplex != "FDD" {
		t.Fatalf("network mode fields missing: %+v", s)
	}
	if s.RadioBand == nil || *s.RadioBand != "LTE BAND 8" || s.RadioChannel == nil || *s.RadioChannel != 3740 {
		t.Fatalf("radio fields missing: %+v", s)
	}
	if s.RegStatus == nil || *s.RegStatus != 2 || s.RegStatusText == nil || *s.RegStatusText != "搜索中" {
		t.Fatalf("registration fields missing: %+v", s)
	}
}

func TestReadATRadioSnapshotDoesNotClearMissingFields(t *testing.T) {
	q := &atRadioSnapshotTestQuerier{
		csqRSSI:     22,
		csqDBM:      -75,
		regStatus:   2,
		regText:     "搜索中",
		cellErr:     errors.New("not ready"),
		radioErr:    errors.New("not ready"),
		operatorErr: errors.New("not ready"),
	}

	s := ReadATRadioSnapshot(context.Background(), q, ATRadioReadOptions{Attempts: 1})

	if s.SignalDBM == nil || *s.SignalDBM != -75 {
		t.Fatalf("SignalDBM=%v want -75", s.SignalDBM)
	}
	if s.RadioBand != nil || s.RadioChannel != nil || s.NetworkMode != nil {
		t.Fatalf("missing radio fields must remain nil, got %+v", s)
	}
	if s.RegStatus == nil || *s.RegStatus != 2 {
		t.Fatalf("RegStatus=%v want 2", s.RegStatus)
	}
}

func TestATRadioSnapshotApplyToStatusOnlyWritesPresentFields(t *testing.T) {
	status := modem.DeviceStatus{
		SignalDBM:     -80,
		SignalRSRP:    -104,
		SignalRSRQ:    -8,
		SignalSINR:    13,
		NetworkDuplex: "FDD",
		NetworkMode:   "LTE",
		RadioBand:     "LTE BAND 8",
		RadioChannel:  3740,
		RegStatus:     2,
		RegStatusText: "搜索中",
	}
	nextDBM := -75
	nextReg := 0
	nextRegText := "未注册"
	snapshot := ATRadioSnapshot{
		SignalDBM:     &nextDBM,
		RegStatus:     &nextReg,
		RegStatusText: &nextRegText,
	}

	got := snapshot.ApplyToStatus(status)

	if got.SignalDBM != -75 || got.RegStatus != 0 || got.RegStatusText != "未注册" {
		t.Fatalf("present fields not applied: %+v", got)
	}
	if got.SignalRSRP != -104 || got.SignalRSRQ != -8 || got.SignalSINR != 13 {
		t.Fatalf("missing signal details should be preserved: %+v", got)
	}
	if got.NetworkMode != "LTE" || got.NetworkDuplex != "FDD" || got.RadioBand != "LTE BAND 8" || got.RadioChannel != 3740 {
		t.Fatalf("missing radio access fields should be preserved: %+v", got)
	}
}
