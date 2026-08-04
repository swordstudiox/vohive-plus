package mbimcore

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

// ReadSIMEF 应先用 APPLICATION_LIST 解析完整 USIM AID,再用该完整 AID 直接调用
// READ_BINARY(CID 9)读取——不开逻辑通道、不发裸 APDU。fake 把 OPEN_CHANNEL/
// UICC_APDU/CLOSE_CHANNEL/READ_RECORD 全部答复为 status=0x9(NoDeviceSupport),
// 如果生产代码仍调用其中任何一个,会因拿到不可用的结果而失败。
func TestManagerReadSIMEFUsesApplicationListFullAIDAndDirectReadBinary(t *testing.T) {
	efData := []byte{0x01, 'A', 'T', '&', 'T'}
	usimFull := []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02, 0xFF, 0xFF, 0xFF, 0xFF, 0x89, 0x07, 0x09, 0x00, 0x00}
	tr := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		return mbim.TestAnswerUICCApplicationListAndReadBinary(w, usimFull, efData)
	})
	m := New("/dev/cdc-wdm0", "auto")
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	got, err := m.ReadSIMEF(context.Background(), 0x6F46, 0)
	if err != nil {
		t.Fatalf("ReadSIMEF() error = %v", err)
	}
	if !bytes.Equal(got, efData) {
		t.Fatalf("ReadSIMEF = % X, want % X", got, efData)
	}
}

// EF_DIR 回退路径:APPLICATION_LIST 返回 status=0x9(固件未实现该 CID)时,
// ReadSIMEF 应回退到直接用 READ_RECORD(CID 10,AID 为空、绝对路径 3F00/2F00)读
// EF_DIR 解析完整 AID(不开逻辑通道、不发裸 APDU),再用该 AID 调用 READ_BINARY
// (CID 9)读取目标 EF。
func TestManagerReadSIMEFFallsBackToEFDIRViaDirectReadRecordWhenApplicationListUnsupported(t *testing.T) {
	efData := []byte{0x01, 'A', 'T', '&', 'T'}
	usimFull := []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02, 0xFF, 0xFF, 0xFF, 0xFF, 0x89, 0x07, 0x09, 0x00, 0x00}
	tr := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		return mbim.TestAnswerUICCEFDIRReadRecordThenReadBinary(w, usimFull, efData)
	})
	m := New("/dev/cdc-wdm0", "auto")
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	got, err := m.ReadSIMEF(context.Background(), 0x6F46, 0)
	if err != nil {
		t.Fatalf("ReadSIMEF() error = %v", err)
	}
	if !bytes.Equal(got, efData) {
		t.Fatalf("ReadSIMEF = % X, want % X", got, efData)
	}
}

// 当 APPLICATION_LIST 和 EF_DIR 扫描都找不到匹配 AID 时,ReadSIMEF 必须直接报错,
// 不能回退去用短 AID(7 字节规范前缀)开逻辑通道——这颗 EM7430 上短 AID 开通道
// 100% 以 status=0x87430002(SelectFailed)失败,回退没有意义。fake 把
// OPEN_CHANNEL/READ_BINARY/UICC_APDU/CLOSE_CHANNEL 全部答复为 status=0x9,
// 用错误信息证明失败原因是"AID 解析失败"而不是某个被静默尝试又失败的开通道调用。
func TestManagerReadSIMEFErrorsWithoutFallingBackToShortAID(t *testing.T) {
	tr := mbim.NewFakeTransport(mbim.TestAnswerUICCNoAIDFound)
	m := New("/dev/cdc-wdm0", "auto")
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	_, err := m.ReadSIMEF(context.Background(), 0x6F46, 0)
	if err == nil {
		t.Fatal("ReadSIMEF() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "EF_DIR") {
		t.Fatalf("ReadSIMEF() error = %q, want it to mention EF_DIR AID resolution failure (not a channel/short-AID attempt)", err.Error())
	}
}

// EF_SPN 等文件直接挂在 ADF_USIM 根下，session 已用 session_type=0 隐式选中当前
// 激活的 USIM；QMI READ_TRANSPARENT 的文件路径必须留空，让模组在已选中的 ADF
// 上下文里直接按 FID 查找。真机验证过给一段编造的路径(如 3F00,7FFF)会被模组
// 拒绝，报 qmi_error=0x0010(NOT_PROVISIONED)。本测试锁定这个行为，防止回归。
func TestManagerReadSIMEFFallsBackToQMITransparentWhenReadBinaryUnsupported(t *testing.T) {
	efData := []byte{0x01, 'A', 'T', '&', 'T'}
	usimFull := []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02, 0xFF, 0xFF, 0xFF, 0xFF, 0x89, 0x07, 0x09, 0x00, 0x00}
	dsInfo := mbim.TestDeviceServicesInfo([]struct {
		Svc  mbim.UUID
		CIDs []uint32
	}{
		{mbim.UUIDBasicConnect, []uint32{1, 9, 11, 16}},
		{mbim.UUIDQMI, []uint32{mbim.CIDQMIMsg}},
	})
	var gotPathLen = -1
	var gotSessionType = byte(0xFF)
	var gotSessionAID []byte
	tr := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		h, _ := mbim.DecodeHeaderForTest(w)
		if h.Type == mbim.MessageTypeOpen {
			return mbim.BuildOpenDoneForTest(h.TransactionID), true
		}
		if h.Type != mbim.MessageTypeCommand {
			return nil, false
		}
		svc := mbim.UUID{}
		copy(svc[:], w[20:36])
		cid := mbim.ReadU32ForTest(w[36:40])
		switch {
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectDeviceServices:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, dsInfo), true
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectDeviceServiceSubscribeList:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, nil), true
		case svc.Equal(mbim.UUIDMSUICCLowLevelAccess) && cid == mbim.CIDUICCApplicationList:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.BuildTestSingleAppListInfoForTest(usimFull)), true
		case svc.Equal(mbim.UUIDMSUICCLowLevelAccess) && cid == mbim.CIDUICCReadBinary:
			return mbim.BuildCommandDoneStatusForTest(h.TransactionID, svc, cid, 0x9, nil), true
		case svc.Equal(mbim.UUIDQMI) && cid == mbim.CIDQMIMsg:
			payload := w[48:]
			qmiSvc := payload[4]
			msgIDOff := 9
			if qmiSvc == 0x00 {
				msgIDOff = 8
			}
			msgID := mbim.ReadU16ForTest(payload[msgIDOff : msgIDOff+2])
			switch {
			case qmiSvc == 0x00 && msgID == 0x0022:
				return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.TestQMIAllocateUIMClientResp(0x01)), true
			case qmiSvc == 0x0B && msgID == 0x0020:
				tlvs := payload[13:]
				// session TLV：[0x01,lenLo,lenHi,sessionType,aidLen,aid...]，长度可变(取决于 AID)。
				if len(tlvs) >= 5 && tlvs[0] == 0x01 {
					sessLen := int(mbim.ReadU16ForTest(tlvs[1:3]))
					sessVal := tlvs[3 : 3+sessLen]
					gotSessionType = sessVal[0]
					aidLen := int(sessVal[1])
					gotSessionAID = append([]byte(nil), sessVal[2:2+aidLen]...)
					// file TLV 紧跟 session TLV 之后：[0x02,lenLo,lenHi,fidLo,fidHi,pathLen,...]
					fileTLV := tlvs[3+sessLen:]
					if len(fileTLV) >= 6 && fileTLV[0] == 0x02 {
						gotPathLen = int(fileTLV[5])
					}
				}
				return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.TestQMIReadResp(0x0020, efData, 0x90, 0x00)), true
			case qmiSvc == 0x00 && msgID == 0x0023:
				return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.TestQMIRelClientResp()), true
			}
		}
		return nil, false
	})
	m := New("/dev/cdc-wdm0", "auto")
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()
	got, err := m.ReadSIMEF(context.Background(), 0x6F46, 0)
	if err != nil {
		t.Fatalf("ReadSIMEF() error = %v", err)
	}
	if !bytes.Equal(got, efData) {
		t.Fatalf("ReadSIMEF = % X, want % X", got, efData)
	}
	if gotPathLen != 0 {
		t.Fatalf("QMI READ_TRANSPARENT 文件路径长度 = %d，want 0(ADF_USIM 子文件应留空路径)", gotPathLen)
	}
	if !bytes.Equal(gotSessionAID, usimFull) {
		t.Fatalf("QMI READ_TRANSPARENT session AID = % X，want % X(必须显式传完整 AID)", gotSessionAID, usimFull)
	}
	if gotSessionType != 0x04 {
		t.Fatalf("QMI READ_TRANSPARENT session_type = 0x%02X，want 0x04(Non-provisioning slot 1；EM7430 拒绝 0x00 Primary GW)", gotSessionType)
	}
}

// 当原生 READ_BINARY 返回 status=0x9(NoDeviceSupport)、且设备未广播 UUIDQMI
// 服务(即不具备 QMI over MBIM 隧道兜底能力)时,ReadSIMEF 必须直接返回明确的
// "不支持且无 QMI 兜底" 错误,不能静默尝试 QMI 调用或返回模糊错误。
func TestManagerReadSIMEFErrorsWhenReadBinaryUnsupportedAndNoQMI(t *testing.T) {
	usimFull := []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02, 0xFF, 0xFF, 0xFF, 0xFF, 0x89, 0x07, 0x09, 0x00, 0x00}
	dsInfo := mbim.TestDeviceServicesInfo([]struct {
		Svc  mbim.UUID
		CIDs []uint32
	}{
		{mbim.UUIDBasicConnect, []uint32{1, 9, 11, 16}},
	})
	tr := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		h, _ := mbim.DecodeHeaderForTest(w)
		if h.Type == mbim.MessageTypeOpen {
			return mbim.BuildOpenDoneForTest(h.TransactionID), true
		}
		if h.Type != mbim.MessageTypeCommand {
			return nil, false
		}
		svc := mbim.UUID{}
		copy(svc[:], w[20:36])
		cid := mbim.ReadU32ForTest(w[36:40])
		switch {
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectDeviceServices:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, dsInfo), true
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectDeviceServiceSubscribeList:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, nil), true
		case svc.Equal(mbim.UUIDMSUICCLowLevelAccess) && cid == mbim.CIDUICCApplicationList:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.BuildTestSingleAppListInfoForTest(usimFull)), true
		case svc.Equal(mbim.UUIDMSUICCLowLevelAccess) && cid == mbim.CIDUICCReadBinary:
			return mbim.BuildCommandDoneStatusForTest(h.TransactionID, svc, cid, 0x9, nil), true
		}
		return nil, false
	})
	m := New("/dev/cdc-wdm0", "auto")
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	_, err := m.ReadSIMEF(context.Background(), 0x6F46, 0)
	if err == nil {
		t.Fatal("ReadSIMEF() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "QMI") {
		t.Fatalf("ReadSIMEF() error = %q, want it to mention missing QMI fallback", err.Error())
	}
}

func TestManagerReadSIMEFFallsBackToQMIReadRecordWhenReadRecordUnsupported(t *testing.T) {
	efData := []byte{0x01, 'A', 'T', '&', 'T'}
	usimFull := []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02, 0xFF, 0xFF, 0xFF, 0xFF, 0x89, 0x07, 0x09, 0x00, 0x00}
	dsInfo := mbim.TestDeviceServicesInfo([]struct {
		Svc  mbim.UUID
		CIDs []uint32
	}{
		{mbim.UUIDBasicConnect, []uint32{1, 9, 11, 16}},
		{mbim.UUIDQMI, []uint32{mbim.CIDQMIMsg}},
	})
	efDirRecord := append([]byte{0x61, byte(2 + len(usimFull))}, append([]byte{0x4F, byte(len(usimFull))}, usimFull...)...)
	tr := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		h, _ := mbim.DecodeHeaderForTest(w)
		if h.Type == mbim.MessageTypeOpen {
			return mbim.BuildOpenDoneForTest(h.TransactionID), true
		}
		if h.Type != mbim.MessageTypeCommand {
			return nil, false
		}
		svc := mbim.UUID{}
		copy(svc[:], w[20:36])
		cid := mbim.ReadU32ForTest(w[36:40])
		switch {
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectDeviceServices:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, dsInfo), true
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectDeviceServiceSubscribeList:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, nil), true
		case svc.Equal(mbim.UUIDMSUICCLowLevelAccess) && cid == mbim.CIDUICCApplicationList:
			return mbim.BuildCommandDoneStatusForTest(h.TransactionID, svc, cid, 0x9, nil), true
		case svc.Equal(mbim.UUIDMSUICCLowLevelAccess) && cid == mbim.CIDUICCReadRecord:
			return mbim.BuildCommandDoneStatusForTest(h.TransactionID, svc, cid, 0x9, nil), true
		case svc.Equal(mbim.UUIDMSUICCLowLevelAccess) && cid == mbim.CIDUICCReadBinary:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.BuildUICCFileResponseInfoForTest(0x90, 0x00, efData)), true
		case svc.Equal(mbim.UUIDQMI) && cid == mbim.CIDQMIMsg:
			payload := w[48:]
			qmiSvc := payload[4]
			msgIDOff := 9
			if qmiSvc == 0x00 {
				msgIDOff = 8
			}
			msgID := mbim.ReadU16ForTest(payload[msgIDOff : msgIDOff+2])
			switch {
			case qmiSvc == 0x00 && msgID == 0x0022:
				return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.TestQMIAllocateUIMClientResp(0x01)), true
			case qmiSvc == 0x0B && msgID == 0x0021:
				return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.TestQMIReadResp(0x0021, efDirRecord, 0x90, 0x00)), true
			case qmiSvc == 0x00 && msgID == 0x0023:
				return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.TestQMIRelClientResp()), true
			}
		}
		return nil, false
	})
	m := New("/dev/cdc-wdm0", "auto")
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()
	got, err := m.ReadSIMEF(context.Background(), 0x6F46, 0)
	if err != nil {
		t.Fatalf("ReadSIMEF() error = %v", err)
	}
	if !bytes.Equal(got, efData) {
		t.Fatalf("ReadSIMEF = % X, want % X", got, efData)
	}
}

func TestManagerReadSIMRecordEFUsesDirectReadRecord(t *testing.T) {
	efData := []byte{0x01, 'A', 'T', '&', 'T'}
	usimFull := []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02, 0xFF, 0xFF, 0xFF, 0xFF, 0x89, 0x07, 0x09, 0x00, 0x00}
	tr := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		h, _ := mbim.DecodeHeaderForTest(w)
		if h.Type == mbim.MessageTypeOpen {
			return mbim.BuildOpenDoneForTest(h.TransactionID), true
		}
		if h.Type != mbim.MessageTypeCommand {
			return nil, false
		}
		svc := mbim.UUID{}
		copy(svc[:], w[20:36])
		cid := mbim.ReadU32ForTest(w[36:40])
		switch {
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectDeviceServiceSubscribeList:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, nil), true
		case svc.Equal(mbim.UUIDMSUICCLowLevelAccess) && cid == mbim.CIDUICCApplicationList:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.BuildTestSingleAppListInfoForTest(usimFull)), true
		case svc.Equal(mbim.UUIDMSUICCLowLevelAccess) && cid == mbim.CIDUICCReadRecord:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.BuildUICCFileResponseInfoForTest(0x90, 0x00, efData)), true
		}
		return nil, false
	})
	m := New("/dev/cdc-wdm0", "auto")
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	data, err := m.ReadSIMRecordEF(context.Background(), 0x6FC5, 1)
	if err != nil {
		t.Fatalf("ReadSIMRecordEF error = %v", err)
	}
	if !bytes.Equal(data, efData) {
		t.Fatalf("ReadSIMRecordEF data = %x, want %x", data, efData)
	}
}

func TestManagerReadSIMRecordEFFallsBackToQMIWhenReadRecordUnsupported(t *testing.T) {
	efData := []byte{0x01, 'A', 'T', '&', 'T'}
	usimFull := []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02, 0xFF, 0xFF, 0xFF, 0xFF, 0x89, 0x07, 0x09, 0x00, 0x00}
	dsInfo := mbim.TestDeviceServicesInfo([]struct {
		Svc  mbim.UUID
		CIDs []uint32
	}{
		{mbim.UUIDBasicConnect, []uint32{1, 9, 11, 16}},
		{mbim.UUIDQMI, []uint32{mbim.CIDQMIMsg}},
	})
	tr := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		h, _ := mbim.DecodeHeaderForTest(w)
		if h.Type == mbim.MessageTypeOpen {
			return mbim.BuildOpenDoneForTest(h.TransactionID), true
		}
		if h.Type != mbim.MessageTypeCommand {
			return nil, false
		}
		svc := mbim.UUID{}
		copy(svc[:], w[20:36])
		cid := mbim.ReadU32ForTest(w[36:40])
		switch {
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectDeviceServices:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, dsInfo), true
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectDeviceServiceSubscribeList:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, nil), true
		case svc.Equal(mbim.UUIDMSUICCLowLevelAccess) && cid == mbim.CIDUICCApplicationList:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.BuildTestSingleAppListInfoForTest(usimFull)), true
		case svc.Equal(mbim.UUIDMSUICCLowLevelAccess) && cid == mbim.CIDUICCReadRecord:
			return mbim.BuildCommandDoneStatusForTest(h.TransactionID, svc, cid, 0x9, nil), true
		case svc.Equal(mbim.UUIDQMI) && cid == mbim.CIDQMIMsg:
			payload := w[48:]
			qmiSvc := payload[4]
			msgIDOff := 9
			if qmiSvc == 0x00 {
				msgIDOff = 8
			}
			msgID := mbim.ReadU16ForTest(payload[msgIDOff : msgIDOff+2])
			switch {
			case qmiSvc == 0x00 && msgID == 0x0022:
				return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.TestQMIAllocateUIMClientResp(0x01)), true
			case qmiSvc == 0x0B && msgID == 0x0021:
				return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.TestQMIReadResp(0x0021, efData, 0x90, 0x00)), true
			case qmiSvc == 0x00 && msgID == 0x0023:
				return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, mbim.TestQMIRelClientResp()), true
			}
		}
		return nil, false
	})
	m := New("/dev/cdc-wdm0", "auto")
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	data, err := m.ReadSIMRecordEF(context.Background(), 0x6FC5, 1)
	if err != nil {
		t.Fatalf("ReadSIMRecordEF error = %v", err)
	}
	if !bytes.Equal(data, efData) {
		t.Fatalf("data = %x, want %x", data, efData)
	}
}
