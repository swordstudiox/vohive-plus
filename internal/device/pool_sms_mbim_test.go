package device

import (
	"context"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/pkg/smscodec"
)

func TestSMSModeMBIMString(t *testing.T) {
	if smsModeMBIM.String() != "MBIM" {
		t.Fatalf("smsModeMBIM.String() = %q, want MBIM", smsModeMBIM.String())
	}
}

func TestDecodeMBIMDeliverPDU(t *testing.T) {
	pduHex := "00" + "04038101F100006250724190410A3754747A0E4ABBCD6F793B4C4FBFDDA0F41CE47ED341617B38CD0E8BD96590F92D07E5DF7539283C1EBFEB6E3A889E87971B"
	sender, text, ts, concat, err := decodeMBIMDeliverPDUHex(pduHex)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if sender != "101" {
		t.Fatalf("sender=%q want 101", sender)
	}
	if text != "This information is not available for your account type" {
		t.Fatalf("text=%q", text)
	}
	if ts.IsZero() {
		t.Fatal("timestamp should be populated")
	}
	if concat.IsConcat {
		t.Fatalf("concat=%+v, want non-concat", concat)
	}
}

func TestDecodeMBIMDeliverPDUConcatMetadata(t *testing.T) {
	pduHex := smscodecFixedSlotPaddedPDU()
	sender, text, ts, concat, err := decodeMBIMDeliverPDUHex(pduHex)
	if err != nil {
		t.Fatalf("decodeMBIMDeliverPDUHex() error = %v", err)
	}
	if sender == "" || text == "" || ts.IsZero() {
		t.Fatalf("unexpected decode result sender=%q text=%q ts=%v", sender, text, ts)
	}
	if !concat.IsConcat || concat.Total != 4 || concat.Seq != 2 {
		t.Fatalf("concat=%+v, want total=4 seq=2", concat)
	}
}

type mbimInboxNotifierStub struct {
	sms []backend.SMS
}

func (s *mbimInboxNotifierStub) NotifySMS(deviceID, sender, content string, timestamp time.Time) {
	s.sms = append(s.sms, backend.SMS{Sender: sender, Content: content, Timestamp: timestamp})
}
func (s *mbimInboxNotifierStub) NotifyIPRotated(deviceID, oldIP, newIP string, duration time.Duration) {
}
func (s *mbimInboxNotifierStub) NotifyRaw(msg string) {}

type mbimInboxBackendStub struct {
	list       []backend.SMSSummary
	readByIdx  map[int]*backend.SMS
	deletedIdx []int
}

func (s *mbimInboxBackendStub) Mode() string                                { return backend.BackendMBIM }
func (s *mbimInboxBackendStub) Close() error                                { return nil }
func (s *mbimInboxBackendStub) GetIMEI(context.Context) (string, error)     { return "", nil }
func (s *mbimInboxBackendStub) GetIMSI(context.Context) (string, error)     { return "", nil }
func (s *mbimInboxBackendStub) GetICCID(context.Context) (string, error)    { return "", nil }
func (s *mbimInboxBackendStub) GetMSISDN(context.Context) (string, error)   { return "", nil }
func (s *mbimInboxBackendStub) GetRevision(context.Context) (string, error) { return "", nil }
func (s *mbimInboxBackendStub) GetSignalInfo(context.Context) (*backend.SignalInfo, error) {
	return nil, nil
}
func (s *mbimInboxBackendStub) GetServingSystem(context.Context) (*backend.ServingSystem, error) {
	return nil, nil
}
func (s *mbimInboxBackendStub) IsSimInserted(context.Context) (bool, error) { return true, nil }
func (s *mbimInboxBackendStub) GetNativeMCCMNC(context.Context) (string, string, error) {
	return "", "", nil
}
func (s *mbimInboxBackendStub) GetNativeSPN(context.Context) (string, error) { return "", nil }
func (s *mbimInboxBackendStub) GetSIMMetadata(context.Context) (*backend.SIMMetadata, error) {
	return nil, nil
}
func (s *mbimInboxBackendStub) SetOperatingMode(context.Context, backend.OperatingMode) error {
	return nil
}
func (s *mbimInboxBackendStub) GetOperatingMode(context.Context) (backend.OperatingMode, error) {
	return backend.ModeOnline, nil
}
func (s *mbimInboxBackendStub) Reboot(context.Context) error { return nil }
func (s *mbimInboxBackendStub) OpenLogicalChannel(context.Context, string) (int, error) {
	return 0, nil
}
func (s *mbimInboxBackendStub) CloseLogicalChannel(context.Context, int) error { return nil }
func (s *mbimInboxBackendStub) TransmitAPDU(context.Context, int, string) (string, error) {
	return "", nil
}
func (s *mbimInboxBackendStub) SendSMS(context.Context, string, string) error { return nil }
func (s *mbimInboxBackendStub) DeleteAllSMS(context.Context) error            { return nil }

func (s *mbimInboxBackendStub) ListSMS(context.Context) ([]backend.SMSSummary, error) {
	return append([]backend.SMSSummary(nil), s.list...), nil
}
func (s *mbimInboxBackendStub) ReadSMS(_ context.Context, index int) (*backend.SMS, error) {
	return s.readByIdx[index], nil
}
func (s *mbimInboxBackendStub) DeleteSMS(_ context.Context, index int) error {
	s.deletedIdx = append(s.deletedIdx, index)
	return nil
}

func TestDrainMBIMInboxReassemblesAndDeletesSegments(t *testing.T) {
	pduHex := smscodecFixedSlotPaddedPDU()
	sender, part2, ts, concat, err := decodeMBIMDeliverPDUHex(pduHex)
	if err != nil {
		t.Fatalf("decodeMBIMDeliverPDUHex() error = %v", err)
	}
	if !concat.IsConcat {
		t.Fatal("fixture should be concat")
	}

	notifier := &mbimInboxNotifierStub{}
	pool := &Pool{notifier: notifier}
	backendStub := &mbimInboxBackendStub{
		list: []backend.SMSSummary{{Index: 1, Tag: 1}},
		readByIdx: map[int]*backend.SMS{
			1: {Index: 1, Content: pduHex},
		},
	}
	w := &Worker{
		ID:          "dev1",
		Backend:     backendStub,
		Pool:        pool,
		reassembler: smscodec.NewReassembler(),
	}
	w.reassembler.Add(sender, smscodec.ConcatInfo{IsConcat: true, Ref: concat.Ref, Total: 4, Seq: 1}, "part1-")
	w.reassembler.Add(sender, smscodec.ConcatInfo{IsConcat: true, Ref: concat.Ref, Total: 4, Seq: 3}, "-part3")
	w.reassembler.Add(sender, smscodec.ConcatInfo{IsConcat: true, Ref: concat.Ref, Total: 4, Seq: 4}, "-part4")

	w.drainMBIMInbox(context.Background(), "test")

	if len(notifier.sms) != 1 {
		t.Fatalf("notifier sms count = %d, want 1", len(notifier.sms))
	}
	if notifier.sms[0].Sender != sender {
		t.Fatalf("sender = %q, want %q", notifier.sms[0].Sender, sender)
	}
	if notifier.sms[0].Content != "part1-"+part2+"-part3-part4" {
		t.Fatalf("content = %q", notifier.sms[0].Content)
	}
	if notifier.sms[0].Timestamp.IsZero() || !notifier.sms[0].Timestamp.Equal(ts) {
		t.Fatalf("timestamp = %v, want decoded timestamp %v", notifier.sms[0].Timestamp, ts)
	}
	if len(backendStub.deletedIdx) != 1 || backendStub.deletedIdx[0] != 1 {
		t.Fatalf("deletedIdx = %v, want [1]", backendStub.deletedIdx)
	}
}

func smscodecFixedSlotPaddedPDU() string {
	return "" +
		"0791448720003023400ED0E7B4D97C0E9BCD000062500221230140A00500036A0402CAA0B49B5E96BBCB741DE81C369B5DEC" +
		"FC8B2E0FDBCBEC3099FC76CF158A6198CD9E83C6EF391D1488B960AF76DA5DA79741F437A81D5E9741613719242F8FCB697B" +
		"D905A296F1F439282C2F8366303888FE06CDCB6E32485CA783CCF27219447F83E4E571396D2FBB40C4303D0C4ACF41637458" +
		"7E2E9341613A480683BF9A429742617CCB410000000000000000000000000000000000000000000000000000000000000000" +
		"0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000" +
		"0000000000"
}
