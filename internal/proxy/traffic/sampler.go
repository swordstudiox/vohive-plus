package traffic

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/swordstudiox/vohive-plus/internal/db"
	"github.com/swordstudiox/vohive-plus/internal/device"
	"github.com/swordstudiox/vohive-plus/internal/proxy/server"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

const (
	trafficCounterSourceQMIWDS = "qmi_wds"

	trafficCounterReadTimeout = 3 * time.Second

	qmiWDSTrafficStatsMask = qmi.WDSPacketStatsTxBytesOK | qmi.WDSPacketStatsRxBytesOK
)

type Sampler struct {
	ctx    context.Context
	cancel context.CancelFunc

	pool *device.Pool
	mgr  *server.Manager

	lastIface       map[string]trafficCounters
	ifaceReadErrLog map[string]time.Time

	workerInterfaces func() []workerInterface
}

type Options struct {
	Pool *device.Pool
	Mgr  *server.Manager

	workerInterfaces func() []workerInterface
}

type workerInterface struct {
	id           string
	iface        string
	source       string
	networkReady func() bool
	readCounters func(context.Context) (trafficCounters, error)
}

type trafficCounters struct {
	RXBytes uint64
	TXBytes uint64
}

func New(opts Options) *Sampler {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Sampler{
		ctx:              ctx,
		cancel:           cancel,
		pool:             opts.Pool,
		mgr:              opts.Mgr,
		lastIface:        make(map[string]trafficCounters),
		ifaceReadErrLog:  make(map[string]time.Time),
		workerInterfaces: opts.workerInterfaces,
	}
	if s.workerInterfaces == nil {
		s.workerInterfaces = s.poolWorkerInterfaces
	}
	return s
}

func (s *Sampler) Stop() {
	s.cancel()
}

func (s *Sampler) Start() {
	s.primeIfaceBaselines()
	go s.loop()
}

func (s *Sampler) loop() {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("traffic sampler panic recovered", "err", r)
		}
	}()

	now := time.Now()
	next := now.Truncate(time.Minute).Add(time.Minute)
	timer := time.NewTimer(time.Until(next))
	defer timer.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-timer.C:
		}

		s.sample(next.Add(-time.Minute))

		next = next.Add(time.Minute)
		now = time.Now()
		if now.After(next) {
			next = now.Truncate(time.Minute).Add(time.Minute)
		}
		timer.Reset(time.Until(next))
	}
}

func (s *Sampler) sample(periodStart time.Time) {
	var points []db.TrafficPoint

	for _, wi := range s.workerInterfaces() {
		if wi.id == "" || wi.iface == "" {
			continue
		}
		if !wi.shouldSampleTraffic() {
			s.clearWorkerInterfaceBaseline(wi)
			continue
		}
		cur, source, err := s.readWorkerCounters(wi)
		if err != nil {
			s.logCounterReadError(wi.id, wi.iface, source, err)
			continue
		}
		key := counterBaselineKey(wi.id, wi.iface, source)
		last, ok := s.lastIface[key]
		s.lastIface[key] = cur
		if !ok {
			continue
		}
		drx := int64(cur.RXBytes) - int64(last.RXBytes)
		dtx := int64(cur.TXBytes) - int64(last.TXBytes)
		if drx < 0 {
			drx = 0
		}
		if dtx < 0 {
			dtx = 0
		}
		points = append(points,
			db.TrafficPoint{PeriodStart: periodStart, Resource: "iface", Tag: ifaceBaselineKey(wi.id, wi.iface), Direction: false, TrafficBytes: drx},
			db.TrafficPoint{PeriodStart: periodStart, Resource: "iface", Tag: ifaceBaselineKey(wi.id, wi.iface), Direction: true, TrafficBytes: dtx},
		)
	}

	if s.mgr != nil {
		snaps := s.mgr.SnapshotAndResetTraffic()
		for instID, snap := range snaps {
			if snap.Downlink > 0 {
				points = append(points, db.TrafficPoint{PeriodStart: periodStart, Resource: "proxy_instance", Tag: instID, Direction: false, TrafficBytes: snap.Downlink})
			}
			if snap.Uplink > 0 {
				points = append(points, db.TrafficPoint{PeriodStart: periodStart, Resource: "proxy_instance", Tag: instID, Direction: true, TrafficBytes: snap.Uplink})
			}
		}
	}

	if err := db.UpsertTrafficMinute(points); err != nil {
		logger.Warn("写入流量分钟桶失败", "err", err)
		return
	}

	now := periodStart.Add(time.Minute)
	if now.Minute() == 0 {
		prevHour := now.Truncate(time.Hour).Add(-time.Hour)
		_ = db.RollupToHour(prevHour)
	}
	if now.Hour() == 0 && now.Minute() == 0 {
		prevDay := now.Truncate(24 * time.Hour).Add(-24 * time.Hour)
		_ = db.RollupToDay(prevDay)
	}
	if now.Weekday() == time.Monday && now.Hour() == 0 && now.Minute() == 0 {
		weekStart := startOfWeek(now).Add(-7 * 24 * time.Hour)
		_ = db.RollupToWeek(weekStart)
	}
	if now.Day() == 1 && now.Hour() == 0 && now.Minute() == 0 {
		monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, -1, 0)
		_ = db.RollupToMonth(monthStart)
	}

	if now.Minute() == 0 {
		_ = db.CleanupBefore(
			now,
			24*time.Hour,
			7*24*time.Hour,
			31*24*time.Hour,
			12*7*24*time.Hour,
			24*31*24*time.Hour,
		)
	}
}

func startOfWeek(t time.Time) time.Time {
	tt := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	for tt.Weekday() != time.Monday {
		tt = tt.Add(-24 * time.Hour)
	}
	return tt
}

func (s *Sampler) primeIfaceBaselines() {
	for _, wi := range s.workerInterfaces() {
		if wi.id == "" || wi.iface == "" {
			continue
		}
		if !wi.shouldSampleTraffic() {
			s.clearWorkerInterfaceBaseline(wi)
			continue
		}
		cur, source, err := s.readWorkerCounters(wi)
		if err != nil {
			s.logCounterReadError(wi.id, wi.iface, source, err)
			continue
		}
		s.lastIface[counterBaselineKey(wi.id, wi.iface, source)] = cur
	}
}

func (s *Sampler) poolWorkerInterfaces() []workerInterface {
	if s.pool == nil {
		return nil
	}
	workers := s.pool.GetAllWorkers()
	out := make([]workerInterface, 0, len(workers))
	for _, w := range workers {
		if w == nil {
			continue
		}
		id := strings.TrimSpace(w.ID)
		iface := strings.TrimSpace(w.Config.Interface)
		if id == "" || iface == "" {
			continue
		}
		worker := w
		networkReady := func() bool {
			return worker.Config.NetworkEnabled && worker.NetworkConnected()
		}
		var readCounters func(context.Context) (trafficCounters, error)
		if qmiCore := worker.QMICore; qmiCore != nil {
			readCounters = func(ctx context.Context) (trafficCounters, error) {
				return readQMIWDSTrafficCounters(ctx, qmiCore)
			}
		}
		out = append(out, workerInterface{id: id, iface: iface, source: trafficCounterSourceQMIWDS, networkReady: networkReady, readCounters: readCounters})
	}
	return out
}

func (s *Sampler) readWorkerCounters(wi workerInterface) (trafficCounters, string, error) {
	source := wi.counterSource()
	if wi.readCounters != nil {
		ctx, cancel := context.WithTimeout(s.ctx, trafficCounterReadTimeout)
		defer cancel()
		cur, err := wi.readCounters(ctx)
		if err == nil {
			return cur, source, nil
		}
		return trafficCounters{}, source, err
	}
	return trafficCounters{}, source, errors.New("qmi wds counter reader not available")
}

func (wi workerInterface) counterSource() string {
	source := strings.TrimSpace(wi.source)
	if source == "" {
		return trafficCounterSourceQMIWDS
	}
	return source
}

func (wi workerInterface) shouldSampleTraffic() bool {
	if wi.networkReady == nil {
		return true
	}
	return wi.networkReady()
}

func (s *Sampler) clearWorkerInterfaceBaseline(wi workerInterface) {
	delete(s.lastIface, counterBaselineKey(wi.id, wi.iface, wi.counterSource()))
}

func ifaceBaselineKey(deviceID string, iface string) string {
	return strings.TrimSpace(deviceID) + "@" + strings.TrimSpace(iface)
}

func counterBaselineKey(deviceID string, iface string, source string) string {
	key := ifaceBaselineKey(deviceID, iface)
	source = strings.TrimSpace(source)
	if source == "" {
		return key
	}
	return key + "#" + source
}

func (s *Sampler) logCounterReadError(deviceID string, iface string, source string, err error) {
	key := counterBaselineKey(deviceID, iface, source)
	now := time.Now()
	if last, ok := s.ifaceReadErrLog[key]; ok && now.Sub(last) < 5*time.Minute {
		return
	}
	s.ifaceReadErrLog[key] = now
	logger.Warn("流量采样读取计数器失败", "device", deviceID, "interface", iface, "source", source, "err", err)
}
