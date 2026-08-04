package smsutil

import (
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/smscodec"
	"github.com/warthog618/sms/encoding/tpdu"
)

type RPDUKind = smscodec.RPDUKind
type RPDUInfo = smscodec.RPDUInfo
type ConcatInfo = smscodec.ConcatInfo
type OmaCPCharacteristic = smscodec.OmaCPCharacteristic
type OmaCPConfig = smscodec.OmaCPConfig

const (
	RPDUKindUnknown         = smscodec.RPDUKindUnknown
	RPDUKindData            = smscodec.RPDUKindData
	RPDUKindAck             = smscodec.RPDUKindAck
	RPDUKindError           = smscodec.RPDUKindError
	RPCauseTemporaryFailure = smscodec.RPCauseTemporaryFailure
	WAPPushOmaCPPort        = smscodec.WAPPushOmaCPPort
)

func DecodeBodyMaybeHex(body []byte) ([]byte, error) { return smscodec.DecodeBodyMaybeHex(body) }
func IsHexString(s string) bool                      { return smscodec.IsHexString(s) }
func ParseRPData(body []byte) (byte, []byte, error)  { return smscodec.ParseRPData(body) }
func ClassifyRPDU(body []byte) RPDUInfo              { return smscodec.ClassifyRPDU(body) }
func ParseRPErrorCause(body []byte) (byte, error)    { return smscodec.ParseRPErrorCause(body) }
func ParseRPDataWithAddresses(body []byte) (byte, string, string, []byte, error) {
	return smscodec.ParseRPDataWithAddresses(body)
}
func DecodeAddressValue(v []byte) (string, error) { return smscodec.DecodeAddressValue(v) }
func EncodeAddress(number string) []byte          { return smscodec.EncodeAddress(number) }
func BuildRPData(rpMr byte, tpduBytes []byte, smsc string) []byte {
	return smscodec.BuildRPData(rpMr, tpduBytes, smsc)
}
func BuildRPAck(rpMr byte) []byte               { return smscodec.BuildRPAck(rpMr) }
func BuildRPError(rpMr byte, cause byte) []byte { return smscodec.BuildRPError(rpMr, cause) }
func DecodeDeliverTPDU(tpduBytes []byte) (string, string, time.Time, ConcatInfo, error) {
	return smscodec.DecodeDeliverTPDU(tpduBytes)
}
func IsShortCode(phone string) bool { return smscodec.IsShortCode(phone) }
func BuildSubmitTPDUs(to, text string) ([][]byte, []int, error) {
	return smscodec.BuildSubmitTPDUs(to, text)
}
func ParseATSMSHeaderTPDULength(header string) (int, bool) {
	return smscodec.ParseATSMSHeaderTPDULength(header)
}
func TrimFullPDUHexByATHeader(pduHex, header string) (string, bool) {
	return smscodec.TrimFullPDUHexByATHeader(pduHex, header)
}
func TrimFullPDUHexByTPDULength(pduHex string, tpduLen int) (string, bool) {
	return smscodec.TrimFullPDUHexByTPDULength(pduHex, tpduLen)
}
func TrimDeliverTPDUToDeclaredLength(tpduBytes []byte) ([]byte, bool) {
	return smscodec.TrimDeliverTPDUToDeclaredLength(tpduBytes)
}
func DeliverTPDUDeclaredLength(tpduBytes []byte) (int, bool) {
	return smscodec.DeliverTPDUDeclaredLength(tpduBytes)
}
func IsOmaCPMessage(udh tpdu.UserDataHeader) bool { return smscodec.IsOmaCPMessage(udh) }
func DecodeOmaCPFromTPDU(data []byte) (*OmaCPConfig, error) {
	return smscodec.DecodeOmaCPFromTPDU(data)
}
func FormatOmaCPSummary(cfg *OmaCPConfig) string { return smscodec.FormatOmaCPSummary(cfg) }
