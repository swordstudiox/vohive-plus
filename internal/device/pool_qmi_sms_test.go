package device

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	qmicore "github.com/swordstudiox/vohive-plus/internal/qmi"
	"github.com/swordstudiox/vohive-plus/pkg/smscodec"
)

type qmiSMSCoreStub struct {
	handler    func(storage uint8, index uint32)
	rawHandler func(qmicore.RawSMSIndication)

	listByStorage map[uint8][]struct {
		Index uint32
		Tag   qmi.MessageTagType
	}
	readResults map[string]*qmimanager.DecodedSMS
	readErrors  map[string]error

	readCalls   []string
	listCalls   []string
	deleteCalls []string
	ackCalls    []qmicore.RawSMSIndication
}

func (s *qmiSMSCoreStub) key(storage uint8, index uint32) string {
	return fmt.Sprintf("%d:%d", storage, index)
}

func (s *qmiSMSCoreStub) OnNewSMSWithStorage(handler func(storage uint8, index uint32)) {
	s.handler = handler
}

func (s *qmiSMSCoreStub) OnNewSMSRaw(handler func(qmicore.RawSMSIndication)) {
	s.rawHandler = handler
}

func (s *qmiSMSCoreStub) ListSMS(storageType uint8, tag qmi.MessageTagType) ([]struct {
	Index uint32
	Tag   qmi.MessageTagType
}, error) {
	s.listCalls = append(s.listCalls, fmt.Sprintf("%d:%d", storageType, tag))
	if s.listByStorage == nil {
		return nil, nil
	}
	out := make([]struct {
		Index uint32
		Tag   qmi.MessageTagType
	}, 0)
	for _, msg := range s.listByStorage[storageType] {
		if msg.Tag == tag {
			out = append(out, msg)
		}
	}
	return out, nil
}

func (s *qmiSMSCoreStub) ReadSMS(preferredStorage uint8, index uint32) (*qmimanager.DecodedSMS, error) {
	key := s.key(preferredStorage, index)
	s.readCalls = append(s.readCalls, key)
	if err := s.readErrors[key]; err != nil {
		return nil, err
	}
	if msg := s.readResults[key]; msg != nil {
		return msg, nil
	}
	return nil, errors.New("missing SMS")
}

func (s *qmiSMSCoreStub) WMSDeleteMessage(ctx context.Context, storageType uint8, index uint32) error {
	s.deleteCalls = append(s.deleteCalls, s.key(storageType, index))
	return nil
}

func (s *qmiSMSCoreStub) AckRawSMS(ctx context.Context, info qmicore.RawSMSIndication, success bool) error {
	if success {
		s.ackCalls = append(s.ackCalls, info)
	}
	return nil
}

const qmiRawSMSFixtureFullPDU = "0791448720003023400ED0E7B4D97C0E9BCD000062500221230140A00500036A0402CAA0B49B5E96BBCB741DE81C369B5DECFC8B2E0FDBCBEC3099FC76CF158A6198CD9E83C6EF391D1488B960AF76DA5DA79741F437A81D5E9741613719242F8FCB697BD905A296F1F439282C2F8366303888FE06CDCB6E32485CA783CCF27219447F83E4E571396D2FBB40C4303D0C4ACF416374587E2E9341613A480683BF9A429742617CCB41000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"

func qmiRawSMSFixtureDirectTPDU(t *testing.T) []byte {
	t.Helper()
	raw, err := hex.DecodeString(qmiRawSMSFixtureFullPDU)
	if err != nil {
		t.Fatal(err)
	}
	smscLen := int(raw[0])
	return append([]byte(nil), raw[1+smscLen:]...)
}

func TestHandleNewSMSQMIUsesIndicatedStorageAndDeletesSameStorage(t *testing.T) {
	stub := &qmiSMSCoreStub{
		readResults: map[string]*qmimanager.DecodedSMS{
			"0:7": {
				Index:     7,
				Storage:   0,
				Sender:    "10086",
				Message:   "hello",
				Timestamp: time.Unix(1700000000, 0),
			},
		},
	}
	worker := &Worker{
		ID:          "wwan0",
		Pool:        &Pool{},
		qmiSMS:      stub,
		reassembler: smscodec.NewReassembler(),
	}

	worker.handleNewSMSQMI(0, 7)

	if len(stub.readCalls) != 1 || stub.readCalls[0] != "0:7" {
		t.Fatalf("unexpected read calls: %v", stub.readCalls)
	}
	if len(stub.deleteCalls) != 1 || stub.deleteCalls[0] != "0:7" {
		t.Fatalf("unexpected delete calls: %v", stub.deleteCalls)
	}
}

func TestHandleNewSMSQMIFallsBackToAlternateStorageWhenIndexExistsThere(t *testing.T) {
	stub := &qmiSMSCoreStub{
		listByStorage: map[uint8][]struct {
			Index uint32
			Tag   qmi.MessageTagType
		}{
			1: {{Index: 9, Tag: qmi.TagTypeMTNotRead}},
		},
		readResults: map[string]*qmimanager.DecodedSMS{
			"1:9": {
				Index:     9,
				Storage:   1,
				Sender:    "10086",
				Message:   "fallback",
				Timestamp: time.Unix(1700000000, 0),
			},
		},
		readErrors: map[string]error{
			"0:9": errors.New("storage miss"),
		},
	}
	worker := &Worker{
		ID:          "wwan0",
		Pool:        &Pool{},
		qmiSMS:      stub,
		reassembler: smscodec.NewReassembler(),
	}

	worker.handleNewSMSQMI(0, 9)

	if len(stub.readCalls) != 2 {
		t.Fatalf("unexpected read calls: %v", stub.readCalls)
	}
	if stub.readCalls[0] != "0:9" || stub.readCalls[1] != "1:9" {
		t.Fatalf("unexpected read order: %v", stub.readCalls)
	}
	if len(stub.deleteCalls) != 1 || stub.deleteCalls[0] != "1:9" {
		t.Fatalf("unexpected delete calls: %v", stub.deleteCalls)
	}
}

func TestCheckAllSMSQMIReadsUnreadFromBothStorages(t *testing.T) {
	stub := &qmiSMSCoreStub{
		listByStorage: map[uint8][]struct {
			Index uint32
			Tag   qmi.MessageTagType
		}{
			0: {{Index: 3, Tag: qmi.TagTypeMTNotRead}},
			1: {{Index: 5, Tag: qmi.TagTypeMTNotRead}},
		},
		readResults: map[string]*qmimanager.DecodedSMS{
			"0:3": {
				Index:     3,
				Storage:   0,
				Sender:    "10010",
				Message:   "sim",
				Timestamp: time.Unix(1700000000, 0),
			},
			"1:5": {
				Index:     5,
				Storage:   1,
				Sender:    "10086",
				Message:   "me",
				Timestamp: time.Unix(1700000001, 0),
			},
		},
	}
	worker := &Worker{
		ID:          "wwan0",
		Pool:        &Pool{},
		qmiSMS:      stub,
		reassembler: smscodec.NewReassembler(),
	}

	if err := worker.CheckAllSMSQMI(); err != nil {
		t.Fatalf("CheckAllSMSQMI() error=%v", err)
	}
	if len(stub.deleteCalls) != 2 {
		t.Fatalf("unexpected delete calls: %v", stub.deleteCalls)
	}
}

func TestCheckAllSMSQMICleansReadResidualsFromBothStorages(t *testing.T) {
	stub := &qmiSMSCoreStub{
		listByStorage: map[uint8][]struct {
			Index uint32
			Tag   qmi.MessageTagType
		}{
			0: {
				{Index: 11, Tag: qmi.TagTypeMTRead},
			},
			1: {
				{Index: 13, Tag: qmi.TagTypeMTRead},
			},
		},
	}
	worker := &Worker{
		ID:          "wwan0",
		Pool:        &Pool{},
		qmiSMS:      stub,
		reassembler: smscodec.NewReassembler(),
	}

	if err := worker.CheckAllSMSQMI(); err != nil {
		t.Fatalf("CheckAllSMSQMI() error=%v", err)
	}

	wantListCalls := []string{
		fmt.Sprintf("0:%d", qmi.TagTypeMTNotRead),
		fmt.Sprintf("0:%d", qmi.TagTypeMTRead),
		fmt.Sprintf("1:%d", qmi.TagTypeMTNotRead),
		fmt.Sprintf("1:%d", qmi.TagTypeMTRead),
	}
	if fmt.Sprint(stub.listCalls) != fmt.Sprint(wantListCalls) {
		t.Fatalf("listCalls=%v want %v", stub.listCalls, wantListCalls)
	}
	if len(stub.readCalls) != 0 {
		t.Fatalf("readCalls=%v, want no reprocessing for read residuals", stub.readCalls)
	}
	wantDeleteCalls := []string{"0:11", "1:13"}
	if fmt.Sprint(stub.deleteCalls) != fmt.Sprint(wantDeleteCalls) {
		t.Fatalf("deleteCalls=%v want %v", stub.deleteCalls, wantDeleteCalls)
	}
}

func TestHandleRawSMSQMIProcessesAndAcksDirectPDU(t *testing.T) {
	stub := &qmiSMSCoreStub{}
	worker := &Worker{
		ID:          "wwan0",
		Pool:        &Pool{},
		qmiSMS:      stub,
		reassembler: smscodec.NewReassembler(),
	}

	worker.handleNewSMSRawQMI(qmicore.RawSMSIndication{
		PDU:           qmiRawSMSFixtureDirectTPDU(t),
		AckRequired:   true,
		TransactionID: 0x11223344,
		Format:        0x06,
	})

	if len(stub.ackCalls) != 1 {
		t.Fatalf("ackCalls=%d, want 1", len(stub.ackCalls))
	}
	if stub.ackCalls[0].TransactionID != 0x11223344 {
		t.Fatalf("ack transaction=0x%x, want 0x11223344", stub.ackCalls[0].TransactionID)
	}
}

func TestHandleRawSMSQMIAcksDecodeFailure(t *testing.T) {
	stub := &qmiSMSCoreStub{}
	worker := &Worker{
		ID:          "wwan0",
		Pool:        &Pool{},
		qmiSMS:      stub,
		reassembler: smscodec.NewReassembler(),
	}

	worker.handleNewSMSRawQMI(qmicore.RawSMSIndication{
		PDU:           []byte{0x40, 0x01, 0x02},
		AckRequired:   true,
		TransactionID: 0x55667788,
		Format:        0x06,
	})

	if len(stub.ackCalls) != 1 {
		t.Fatalf("ackCalls=%d, want 1", len(stub.ackCalls))
	}
	if stub.ackCalls[0].TransactionID != 0x55667788 {
		t.Fatalf("ack transaction=0x%x, want 0x55667788", stub.ackCalls[0].TransactionID)
	}
}
