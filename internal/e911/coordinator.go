package e911

import (
	"context"
	"errors"
	"strings"

	"github.com/iniwex5/vowifi-go/runtimehost/carrier"
	runtimee911 "github.com/iniwex5/vowifi-go/runtimehost/e911"
	"github.com/swordstudiox/vohive-plus/internal/device"
	"github.com/swordstudiox/vohive-plus/internal/modem"
	"github.com/swordstudiox/vohive-plus/internal/websheet"
)

// ErrNotSupported means device status does not support e911 updates.
var ErrNotSupported = errors.New("e911 update not supported by current status")

var ErrProviderUnavailable = errors.New("e911 entitlement provider unavailable or unsupported")
var ErrChallengeIncomplete = errors.New("e911 websheet requires cellular authentication")
var ErrCarrierWebsheetAbsent = errors.New("e911 websheet url not provided by carrier")
var ErrIdentityUnavailable = errors.New("identity information unavailable")

type Coordinator struct {
	DeviceID  string
	Pool      *device.Pool
	Websheets *websheet.Broker
}

func NewCoordinator(deviceID string, pool *device.Pool, websheets *websheet.Broker) *Coordinator {
	return &Coordinator{
		DeviceID:  deviceID,
		Pool:      pool,
		Websheets: websheets,
	}
}

func (c *Coordinator) StartWebsheet(ctx context.Context, deviceID string) (websheet.Info, error) {
	if c == nil || c.Pool == nil || c.Websheets == nil {
		return websheet.Info{}, ErrProviderUnavailable
	}

	w := c.Pool.GetWorker(deviceID)
	if w == nil {
		return websheet.Info{}, ErrIdentityUnavailable
	}

	status := w.ProjectDeviceStatus()
	if !SetupAvailable(status) {
		return websheet.Info{}, ErrNotSupported
	}

	mcc, mnc := nativePLMN(status)
	cfg := carrier.ResolveEffectiveCarrierConfig(carrier.EffectiveCarrierConfigInput{
		MCC: mcc,
		MNC: mnc,
	})
	if !cfg.E911.Enabled || strings.TrimSpace(cfg.E911.Provider) == "" {
		return websheet.Info{}, ErrProviderUnavailable
	}

	akaProvider := device.BuildAKAProvider(w, deviceID)

	var req websheet.Request

	entitlementClient := httpClientAdapter{
		deviceID: deviceID,
		client:   runtimee911.NewDefaultHTTPClient(),
	}

	websheetReq, err := runtimee911.StartEmergencyAddressUpdate(ctx, runtimee911.Request{
		Carrier:     cfg,
		Identity:    buildRuntimeE911Identity(status, mcc, mnc, displayName(w)),
		AKAProvider: akaProvider,
		Client:      entitlementClient,
		Trace:       entitlementTraceSink{deviceID: deviceID},
	})
	if err != nil {
		switch {
		case errors.Is(err, runtimee911.ErrUnsupportedProvider):
			return websheet.Info{}, ErrProviderUnavailable
		case errors.Is(err, runtimee911.ErrChallengeNotImplemented):
			return websheet.Info{}, ErrChallengeIncomplete
		case errors.Is(err, runtimee911.ErrWebsheetUnavailable):
			return websheet.Info{}, ErrCarrierWebsheetAbsent
		default:
			return websheet.Info{}, err
		}
	}

	req.URL = websheetReq.URL
	req.UserData = websheetReq.UserData
	req.ContentType = websheetReq.ContentType
	req.Title = websheetReq.Title

	session, err := c.Websheets.Create(ctx, req)
	if err != nil {
		return websheet.Info{}, err
	}

	return session.Info(), nil
}

func displayName(w *device.Worker) string {
	if w == nil {
		return "VoHive"
	}
	cfg := w.Config
	if cfg.Name != "" {
		return cfg.Name
	}
	return "VoHive " + w.ID
}

func buildATTSIPUsername(imsi string) string {
	if imsi == "" {
		return ""
	}
	return imsi + "@private.att.net"
}

func buildRuntimeE911Identity(status modem.DeviceStatus, mcc, mnc, name string) runtimee911.Identity {
	return runtimee911.Identity{
		IMSI:        status.IMSI,
		IMEI:        status.IMEI,
		MCC:         mcc,
		MNC:         mnc,
		SIPUsername: buildATTSIPUsername(status.IMSI),
		DisplayName: name,
	}
}

type httpClientAdapter struct {
	deviceID string
	client   runtimee911.HTTPClient
}

func (a httpClientAdapter) Do(req *runtimee911.HTTPRequest) (*runtimee911.HTTPResponse, error) {
	headers := make([]runtimee911.HeaderPair, len(req.Headers))
	for i, h := range req.Headers {
		headers[i] = runtimee911.HeaderPair{Key: h.Key, Value: h.Value}
	}

	client := a.client
	if client == nil {
		client = runtimee911.NewDefaultHTTPClient()
	}
	resp, err := client.Do(&runtimee911.HTTPRequest{
		Method:  req.Method,
		URL:     req.URL,
		Headers: headers,
		Body:    append([]byte(nil), req.Body...),
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("e911 entitlement HTTP client returned nil response")
	}

	body := append([]byte(nil), resp.Body...)
	return &runtimee911.HTTPResponse{
		StatusCode: resp.StatusCode,
		Body:       body,
	}, nil
}
