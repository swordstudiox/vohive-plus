package traffic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/device"
)

const (
	RealtimeStatusWaitingSample = "waiting_sample"
	RealtimeStatusOK            = "ok"
	RealtimeStatusReset         = "reset"
	RealtimeStatusError         = "error"

	defaultRealtimeInterval = time.Second
)

type RealtimeSnapshot struct {
	DeviceID     string    `json:"device_id"`
	Interface    string    `json:"interface,omitempty"`
	Source       string    `json:"source"`
	Timestamp    time.Time `json:"timestamp"`
	IntervalMS   int64     `json:"interval_ms"`
	RXBPS        int64     `json:"rx_bps"`
	TXBPS        int64     `json:"tx_bps"`
	RXDeltaBytes int64     `json:"rx_delta_bytes"`
	TXDeltaBytes int64     `json:"tx_delta_bytes"`
	TotalRXBytes uint64    `json:"total_rx_bytes"`
	TotalTXBytes uint64    `json:"total_tx_bytes"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
}

type RealtimeOptions struct {
	Pool            *device.Pool
	Interval        time.Duration
	Now             func() time.Time
	ReaderForDevice func(deviceID string) (realtimeCounterReader, error)
}

type realtimeCounterReader struct {
	DeviceID     string
	Interface    string
	ReadCounters func(context.Context) (trafficCounters, error)
}

type RealtimeManager struct {
	pool            *device.Pool
	interval        time.Duration
	now             func() time.Time
	readerForDevice func(deviceID string) (realtimeCounterReader, error)

	mu      sync.Mutex
	streams map[string]*realtimeStream
}

type realtimeStream struct {
	mgr         *RealtimeManager
	deviceID    string
	nextSubID   uint64
	subscribers map[uint64]chan RealtimeSnapshot
	stop        chan struct{}
}

func NewRealtimeManager(opts RealtimeOptions) *RealtimeManager {
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultRealtimeInterval
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	m := &RealtimeManager{
		pool:            opts.Pool,
		interval:        interval,
		now:             now,
		readerForDevice: opts.ReaderForDevice,
		streams:         make(map[string]*realtimeStream),
	}
	if m.readerForDevice == nil {
		m.readerForDevice = m.defaultReaderForDevice
	}
	return m
}

func (m *RealtimeManager) Subscribe(ctx context.Context, deviceID string) (<-chan RealtimeSnapshot, func()) {
	ch := make(chan RealtimeSnapshot, 8)
	if m == nil {
		close(ch)
		return ch, func() {}
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		close(ch)
		return ch, func() {}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	m.mu.Lock()
	stream := m.streams[deviceID]
	startStream := false
	if stream == nil {
		stream = &realtimeStream{
			mgr:         m,
			deviceID:    deviceID,
			subscribers: make(map[uint64]chan RealtimeSnapshot),
			stop:        make(chan struct{}),
		}
		m.streams[deviceID] = stream
		startStream = true
	}
	subID := stream.nextSubID
	stream.nextSubID++
	stream.subscribers[subID] = ch
	m.mu.Unlock()
	if startStream {
		go stream.loop()
	}

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			m.unsubscribe(deviceID, subID)
		})
	}
	if done := ctx.Done(); done != nil {
		go func() {
			<-done
			unsubscribe()
		}()
	}
	return ch, unsubscribe
}

func (m *RealtimeManager) unsubscribe(deviceID string, subID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stream := m.streams[deviceID]
	if stream == nil {
		return
	}
	if ch, ok := stream.subscribers[subID]; ok {
		delete(stream.subscribers, subID)
		close(ch)
	}
	if len(stream.subscribers) == 0 {
		delete(m.streams, deviceID)
		close(stream.stop)
	}
}

func (s *realtimeStream) loop() {
	ticker := time.NewTicker(s.mgr.interval)
	defer ticker.Stop()

	var (
		last     trafficCounters
		lastTime time.Time
		haveLast bool
	)

	select {
	case <-s.stop:
		return
	default:
	}
	s.sample(&last, &lastTime, &haveLast)
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.sample(&last, &lastTime, &haveLast)
		}
	}
}

func (s *realtimeStream) sample(last *trafficCounters, lastTime *time.Time, haveLast *bool) {
	reader, err := s.mgr.readerForDevice(s.deviceID)
	if err != nil {
		*last = trafficCounters{}
		*lastTime = time.Time{}
		*haveLast = false
		s.broadcast(RealtimeSnapshot{
			DeviceID:  s.deviceID,
			Source:    trafficCounterSourceQMIWDS,
			Timestamp: s.mgr.now(),
			Status:    RealtimeStatusError,
			Error:     err.Error(),
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), trafficCounterReadTimeout)
	cur, err := reader.ReadCounters(ctx)
	cancel()

	now := s.mgr.now()
	snap := RealtimeSnapshot{
		DeviceID:     reader.DeviceID,
		Interface:    reader.Interface,
		Source:       trafficCounterSourceQMIWDS,
		Timestamp:    now,
		TotalRXBytes: cur.RXBytes,
		TotalTXBytes: cur.TXBytes,
	}
	if err != nil {
		*last = trafficCounters{}
		*lastTime = time.Time{}
		*haveLast = false
		snap.Status = RealtimeStatusError
		snap.Error = err.Error()
		s.broadcast(snap)
		return
	}

	if !*haveLast {
		snap.Status = RealtimeStatusWaitingSample
		*last = cur
		*lastTime = now
		*haveLast = true
		s.broadcast(snap)
		return
	}

	if cur.RXBytes < last.RXBytes || cur.TXBytes < last.TXBytes {
		snap.Status = RealtimeStatusReset
		*last = cur
		*lastTime = now
		s.broadcast(snap)
		return
	}

	intervalMS := now.Sub(*lastTime).Milliseconds()
	if intervalMS <= 0 {
		intervalMS = s.mgr.interval.Milliseconds()
	}
	if intervalMS <= 0 {
		intervalMS = 1
	}
	rxDelta := int64(cur.RXBytes - last.RXBytes)
	txDelta := int64(cur.TXBytes - last.TXBytes)
	snap.Status = RealtimeStatusOK
	snap.IntervalMS = intervalMS
	snap.RXDeltaBytes = rxDelta
	snap.TXDeltaBytes = txDelta
	snap.RXBPS = rxDelta * 1000 / intervalMS
	snap.TXBPS = txDelta * 1000 / intervalMS

	*last = cur
	*lastTime = now
	s.broadcast(snap)
}

func (s *realtimeStream) broadcast(snap RealtimeSnapshot) {
	s.mgr.mu.Lock()
	defer s.mgr.mu.Unlock()

	if s.mgr.streams[s.deviceID] != s {
		return
	}
	for _, ch := range s.subscribers {
		select {
		case ch <- snap:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- snap:
			default:
			}
		}
	}
}

func (m *RealtimeManager) defaultReaderForDevice(deviceID string) (realtimeCounterReader, error) {
	if m.pool == nil {
		return realtimeCounterReader{}, errors.New("device pool not available")
	}
	w := m.pool.GetWorker(deviceID)
	if w == nil {
		return realtimeCounterReader{}, fmt.Errorf("device %s not found", deviceID)
	}
	if strings.TrimSpace(w.Config.Interface) == "" {
		return realtimeCounterReader{}, fmt.Errorf("device %s interface not configured", deviceID)
	}
	if w.QMICore == nil {
		return realtimeCounterReader{}, fmt.Errorf("device %s qmi core not available", deviceID)
	}
	qmiCore := w.QMICore
	return realtimeCounterReader{
		DeviceID:  strings.TrimSpace(w.ID),
		Interface: strings.TrimSpace(w.Config.Interface),
		ReadCounters: func(ctx context.Context) (trafficCounters, error) {
			return readQMIWDSTrafficCounters(ctx, qmiCore)
		},
	}, nil
}
