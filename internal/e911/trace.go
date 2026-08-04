package e911

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	runtimee911 "github.com/iniwex5/vowifi-go/runtimehost/e911"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

const maxEntitlementTraceBody = 64 * 1024

var (
	traceLongDigitsPattern = regexp.MustCompile(`\d{8,}`)
	traceAuthTokenPattern  = regexp.MustCompile(`(?i)(authtoken(?:%3D|=))([^&\s"]+)`)
)

type entitlementTraceSink struct {
	deviceID string
}

func (s entitlementTraceSink) Request(req *runtimee911.HTTPRequest) {
	traceEntitlementRequest(s.deviceID, req)
}

func (s entitlementTraceSink) Response(req *runtimee911.HTTPRequest, resp *runtimee911.HTTPResponse) {
	if req == nil || resp == nil {
		return
	}
	traceEntitlementResponse(s.deviceID, req.URL, resp.StatusCode, resp.Body)
}

func (s entitlementTraceSink) Error(req *runtimee911.HTTPRequest, err error) {
	if req == nil {
		traceEntitlementError(s.deviceID, "", err)
		return
	}
	traceEntitlementError(s.deviceID, req.URL, err)
}

func traceEntitlementRequest(deviceID string, req *runtimee911.HTTPRequest) {
	if req == nil {
		return
	}
	raw := envFlag("VOHIVE_E911_TRACE_RAW")
	body := decodeEntitlementTraceBody(req.Body)
	logger.RunDebug("E911 entitlement request",
		"device", deviceID,
		"method", req.Method,
		"url", req.URL,
		"trace_raw", raw,
		"body_len", len(body),
		"body", formatEntitlementTraceBody(body, raw))
}

func traceEntitlementResponse(deviceID, url string, statusCode int, body []byte) {
	raw := envFlag("VOHIVE_E911_TRACE_RAW")
	body = decodeEntitlementTraceBody(body)
	logger.RunDebug("E911 entitlement response",
		"device", deviceID,
		"url", url,
		"http_status", statusCode,
		"trace_raw", raw,
		"body_len", len(body),
		"body", formatEntitlementTraceBody(body, raw))
	traceEntitlementPhoneNumberResponses(deviceID, url, body)
}

func traceEntitlementError(deviceID, url string, err error) {
	if err == nil {
		return
	}
	logger.RunWarn("E911 entitlement request failed",
		"device", deviceID,
		"url", url,
		"err", err)
}

func decodeEntitlementTraceBody(body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	r, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return append([]byte(nil), body...)
	}
	defer r.Close()
	plain, err := io.ReadAll(r)
	if err != nil {
		return append([]byte(nil), body...)
	}
	return plain
}

func formatEntitlementTraceBody(body []byte, raw bool) string {
	body = limitEntitlementTraceBody(body)
	if raw {
		return string(body)
	}

	var decoded any
	if err := json.Unmarshal(body, &decoded); err == nil {
		redacted := redactEntitlementTraceValue("", decoded)
		out, err := json.Marshal(redacted)
		if err == nil {
			return string(out)
		}
	}
	return redactEntitlementTraceString(string(body))
}

func traceEntitlementPhoneNumberResponses(deviceID, url string, body []byte) {
	for _, item := range extractEntitlementPhoneNumberResponses(body) {
		logger.RunDebug("E911 entitlement phone number response",
			"device", deviceID,
			"url", url,
			"response_id", item.ResponseID,
			"status", item.Status,
			"phone_number", redactEntitlementTraceString(item.PhoneNumber),
			"phone_number_sha256", traceValueFingerprint(item.PhoneNumber),
			"signature_present", item.Signature != "")
	}
}

type entitlementPhoneNumberResponse struct {
	Status      int    `json:"status"`
	ResponseID  int    `json:"response-id"`
	PhoneNumber string `json:"phone-number"`
	Signature   string `json:"signature"`
}

func extractEntitlementPhoneNumberResponses(body []byte) []entitlementPhoneNumberResponse {
	var items []entitlementPhoneNumberResponse
	if err := json.Unmarshal(body, &items); err != nil {
		return nil
	}
	out := make([]entitlementPhoneNumberResponse, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.PhoneNumber) == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func redactEntitlementTraceValue(key string, value any) any {
	if isSensitiveEntitlementTraceKey(key) {
		return redactedTracePlaceholder(key, value)
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			out[k] = redactEntitlementTraceValue(k, child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = redactEntitlementTraceValue("", child)
		}
		return out
	case string:
		return redactEntitlementTraceString(v)
	default:
		return v
	}
}

func isSensitiveEntitlementTraceKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "token",
		"app-token",
		"payload",
		"subscriber-id",
		"unique-id",
		"sip-username",
		"apns-token",
		"challenge",
		"address-update-url-post-data":
		return true
	default:
		return false
	}
}

func redactedTracePlaceholder(key string, value any) string {
	if s, ok := value.(string); ok {
		out := fmt.Sprintf("[REDACTED len=%d sha256=%s", utf8.RuneCountInString(s), traceValueFingerprint(s))
		if strings.EqualFold(strings.TrimSpace(key), "subscriber-id") {
			if ann := subscriberIDTraceAnnotation(s); ann != "" {
				out += " " + ann
			}
		}
		return out + "]"
	}
	return "[REDACTED]"
}

func traceValueFingerprint(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

func subscriberIDTraceAnnotation(subscriberID string) string {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(subscriberID))
	if err != nil || len(raw) < 6 {
		return ""
	}
	identity := string(raw[5:])
	imsi, mcc, mnc, ok := parseEntitlementNAI(identity)
	if !ok {
		return ""
	}
	return fmt.Sprintf("nai_mcc=%s nai_mnc=%s imsi_sha256=%s", mcc, mnc, traceValueFingerprint(imsi))
}

func parseEntitlementNAI(identity string) (imsi, mcc, mnc string, ok bool) {
	const suffix = ".3gppnetwork.org"
	identity = strings.TrimSpace(identity)
	if !strings.HasSuffix(identity, suffix) {
		return "", "", "", false
	}
	at := strings.Index(identity, "@nai.epc.mnc")
	if at <= 1 {
		return "", "", "", false
	}
	imsi = strings.TrimPrefix(identity[:at], "0")
	rest := strings.TrimSuffix(identity[at+len("@nai.epc.mnc"):], suffix)
	parts := strings.Split(rest, ".mcc")
	if len(parts) != 2 {
		return "", "", "", false
	}
	mnc = strings.TrimSpace(parts[0])
	mcc = strings.TrimSpace(parts[1])
	if imsi == "" || mcc == "" || mnc == "" {
		return "", "", "", false
	}
	return imsi, mcc, mnc, true
}

func redactEntitlementTraceString(s string) string {
	s = traceAuthTokenPattern.ReplaceAllString(s, `${1}[REDACTED]`)
	return traceLongDigitsPattern.ReplaceAllStringFunc(s, maskTraceLongDigits)
}

func maskTraceLongDigits(s string) string {
	if len(s) <= 6 {
		return strings.Repeat("*", len(s))
	}
	return s[:3] + strings.Repeat("*", len(s)-5) + s[len(s)-2:]
}

func limitEntitlementTraceBody(body []byte) []byte {
	if len(body) <= maxEntitlementTraceBody {
		return append([]byte(nil), body...)
	}
	out := append([]byte(nil), body[:maxEntitlementTraceBody]...)
	out = append(out, []byte("...[TRUNCATED]")...)
	return out
}

func envFlag(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
