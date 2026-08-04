package sim

import (
	"errors"
	"fmt"

	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

// BuildUSIMAuthAPDU 构造 USIM AKA 鉴权 APDU（RAND/AUTN），用于逻辑通道下发。
func BuildUSIMAuthAPDU(rand16, autn16 []byte, includeLe bool) ([]byte, error) {
	if len(rand16) != 16 {
		return nil, fmt.Errorf("RAND 长度必须为 16 字节: %d", len(rand16))
	}
	if len(autn16) != 16 {
		return nil, fmt.Errorf("AUTN 长度必须为 16 字节: %d", len(autn16))
	}

	authData := make([]byte, 0, 1+16+1+16)
	authData = append(authData, 0x10)
	authData = append(authData, rand16...)
	authData = append(authData, 0x10)
	authData = append(authData, autn16...)

	apdu := make([]byte, 0, 5+len(authData)+1)
	apdu = append(apdu, 0x00, 0x88, 0x00, 0x81, byte(len(authData)))
	apdu = append(apdu, authData...)
	if includeLe {
		apdu = append(apdu, 0x00)
	}
	return apdu, nil
}

// ParseUSIMAuthResponse 解析 USIM AKA 鉴权响应（支持成功/同步失败等分支）。
func ParseUSIMAuthResponse(deviceID string, resp []byte) (AKAResult, error) {
	if len(resp) == 2 {
		return AKAResult{}, fmt.Errorf("APDU 状态码非 9000: %02X%02X", resp[0], resp[1])
	}
	if len(resp) < 4 {
		return AKAResult{}, fmt.Errorf("响应过短: %d", len(resp))
	}

	sw1 := resp[len(resp)-2]
	sw2 := resp[len(resp)-1]
	body := resp[:len(resp)-2]
	if sw1 != 0x90 || sw2 != 0x00 {
		return AKAResult{}, fmt.Errorf("APDU 状态码非 9000: %02X%02X", sw1, sw2)
	}
	if len(body) < 2 {
		return AKAResult{}, errors.New("响应体过短")
	}

	tag := body[0]
	logger.Debug("USIM AKA 响应解析",
		"device", deviceID,
		"tag", fmt.Sprintf("0x%02X", tag),
		"sw", fmt.Sprintf("%02X%02X", sw1, sw2),
		"body_len", len(body),
		"body", maskHexBytes(body))

	switch tag {
	case 0xDB:
		if out, ok := parseUSIMAuthDB(body); ok {
			logger.Debug("USIM AKA 成功响应已解析",
				"device", deviceID,
				"res_len", len(out.RES), "ck_len", len(out.CK), "ik_len", len(out.IK),
				"res", maskHexBytes(out.RES), "ck", maskHexBytes(out.CK), "ik", maskHexBytes(out.IK))
			return out, nil
		}
		data, err := parseTLVData(body)
		if err != nil {
			return AKAResult{}, err
		}
		if out, ok := parseUSIMAuthDB(append([]byte{0xDB}, data...)); ok {
			logger.Debug("USIM AKA 成功响应已解析（TLV 回退）",
				"device", deviceID,
				"res_len", len(out.RES), "ck_len", len(out.CK), "ik_len", len(out.IK),
				"res", maskHexBytes(out.RES), "ck", maskHexBytes(out.CK), "ik", maskHexBytes(out.IK))
			return out, nil
		}
		return AKAResult{}, errors.New("AKA 成功响应解析失败")

	case 0xDC:
		data, err := parseTLVData(body)
		if err != nil {
			return AKAResult{}, err
		}
		auts := append([]byte(nil), data...)
		logger.Warn("USIM AKA 同步失败，返回 AUTS",
			"device", deviceID,
			"auts_len", len(auts),
			"auts", maskHexBytes(auts))
		return AKAResult{AUTS: auts}, nil

	case 0xDD:
		return AKAResult{}, errors.New("AKA MAC 校验失败")
	default:
		_, err := parseTLVData(body)
		if err != nil {
			return AKAResult{}, err
		}
		return AKAResult{}, fmt.Errorf("未知 AKA 响应 tag: 0x%02X", tag)
	}
}

// parseTLVData 提取 TLV 格式响应中的数据部分。
func parseTLVData(body []byte) ([]byte, error) {
	if len(body) < 2 {
		return nil, errors.New("响应体过短")
	}
	l := int(body[1])
	if len(body) < 2+l {
		return nil, fmt.Errorf("响应体长度不匹配: need=%d have=%d", 2+l, len(body))
	}
	return body[2 : 2+l], nil
}

// parseUSIMAuthDB 解析 0xDB 成功响应，提取 RES/CK/IK。
func parseUSIMAuthDB(body []byte) (AKAResult, bool) {
	if len(body) < 2 || body[0] != 0xDB {
		return AKAResult{}, false
	}

	pos := 1
	resLen := int(body[pos])
	pos++
	if resLen <= 0 || len(body) < pos+resLen+1 {
		return AKAResult{}, false
	}
	res := append([]byte(nil), body[pos:pos+resLen]...)
	pos += resLen

	remain := len(body) - pos
	if remain == 32 {
		ck := append([]byte(nil), body[pos:pos+16]...)
		ik := append([]byte(nil), body[pos+16:pos+32]...)
		return AKAResult{RES: res, CK: ck, IK: ik}, true
	}

	ckLen := int(body[pos])
	pos++
	if ckLen <= 0 || len(body) < pos+ckLen+1 {
		return AKAResult{}, false
	}
	ck := append([]byte(nil), body[pos:pos+ckLen]...)
	pos += ckLen

	ikLen := int(body[pos])
	pos++
	if ikLen <= 0 || len(body) < pos+ikLen {
		return AKAResult{}, false
	}
	ik := append([]byte(nil), body[pos:pos+ikLen]...)
	return AKAResult{RES: res, CK: ck, IK: ik}, true
}

type LogicalChannelTransport interface {
	OpenLogicalChannel(aid string) (int, error)
	CloseLogicalChannel(channel int) error
	TransmitAPDU(channel int, hexAPDU string) (string, error)
}

type LogicalChannelAIDResolver interface {
	ResolveLogicalChannelAID(app string, fallbackAID string) (resolvedAID string, source string, err error)
}
