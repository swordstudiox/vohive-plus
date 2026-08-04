package traffic

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/db"
)

func TestStartPrimesInterfaceBaselineBeforeFirstMinute(t *testing.T) {
	sampler := New(Options{
		workerInterfaces: func() []workerInterface {
			return []workerInterface{{
				id:     "dev-a",
				iface:  "wwan0",
				source: trafficCounterSourceQMIWDS,
				readCounters: func(context.Context) (trafficCounters, error) {
					return trafficCounters{RXBytes: 1000, TXBytes: 500}, nil
				},
			}}
		},
	})

	sampler.Start()
	sampler.Stop()

	got, ok := sampler.lastIface[counterBaselineKey("dev-a", "wwan0", trafficCounterSourceQMIWDS)]
	if !ok {
		t.Fatalf("baseline was not primed for dev-a@wwan0; keys=%v", sampler.lastIface)
	}
	if got.RXBytes != 1000 || got.TXBytes != 500 {
		t.Fatalf("baseline=%+v want rx=1000 tx=500", got)
	}
}

func TestSampleWritesZeroDeltaWhenInterfaceWasRead(t *testing.T) {
	initSamplerTrafficTestDB(t)
	periodStart := time.Date(2026, time.May, 26, 10, 30, 0, 0, time.UTC)
	sampler := New(Options{
		workerInterfaces: func() []workerInterface {
			return []workerInterface{{
				id:     "dev-a",
				iface:  "wwan0",
				source: trafficCounterSourceQMIWDS,
				readCounters: func(context.Context) (trafficCounters, error) {
					return trafficCounters{RXBytes: 1000, TXBytes: 500}, nil
				},
			}}
		},
	})
	sampler.lastIface[counterBaselineKey("dev-a", "wwan0", trafficCounterSourceQMIWDS)] = trafficCounters{RXBytes: 1000, TXBytes: 500}

	sampler.sample(periodStart)

	gotPeriod, rx, tx, err := db.GetLatestMinuteDeltas("iface", "dev-a@wwan0")
	if err != nil {
		t.Fatalf("GetLatestMinuteDeltas() error = %v", err)
	}
	if !gotPeriod.Equal(periodStart) {
		t.Fatalf("period=%s want %s", gotPeriod, periodStart)
	}
	if rx != 0 || tx != 0 {
		t.Fatalf("rx=%d tx=%d want both zero", rx, tx)
	}
}

func TestSampleDoesNotReuseBaselineAcrossInterfaceChange(t *testing.T) {
	initSamplerTrafficTestDB(t)
	periodStart := time.Date(2026, time.May, 26, 10, 30, 0, 0, time.UTC)
	currentIface := "wwan0"
	counters := map[string]trafficCounters{
		"wwan0": {RXBytes: 1000, TXBytes: 500},
		"wwan1": {RXBytes: 9000, TXBytes: 7000},
	}
	sampler := New(Options{
		workerInterfaces: func() []workerInterface {
			iface := currentIface
			return []workerInterface{{
				id:     "dev-a",
				iface:  iface,
				source: trafficCounterSourceQMIWDS,
				readCounters: func(context.Context) (trafficCounters, error) {
					return counters[iface], nil
				},
			}}
		},
	})

	sampler.primeIfaceBaselines()
	currentIface = "wwan1"
	sampler.sample(periodStart)

	gotPeriod, rx, tx, err := db.GetLatestMinuteDeltas("iface", "dev-a@wwan1")
	if err != nil {
		t.Fatalf("GetLatestMinuteDeltas() error = %v", err)
	}
	if !gotPeriod.IsZero() || rx != 0 || tx != 0 {
		t.Fatalf("unexpected traffic after interface switch: period=%s rx=%d tx=%d", gotPeriod, rx, tx)
	}
	got, ok := sampler.lastIface[counterBaselineKey("dev-a", "wwan1", trafficCounterSourceQMIWDS)]
	if !ok {
		t.Fatalf("new interface baseline was not recorded")
	}
	if got.RXBytes != 9000 || got.TXBytes != 7000 {
		t.Fatalf("new baseline=%+v want wwan1 counters", got)
	}
}

func TestSampleUsesWorkerCounterSourceWhenProvided(t *testing.T) {
	initSamplerTrafficTestDB(t)
	periodStart := time.Date(2026, time.May, 26, 10, 30, 0, 0, time.UTC)
	sampler := New(Options{
		workerInterfaces: func() []workerInterface {
			return []workerInterface{{
				id:     "dev-a",
				iface:  "wwan0",
				source: trafficCounterSourceQMIWDS,
				readCounters: func(context.Context) (trafficCounters, error) {
					return trafficCounters{RXBytes: 60984, TXBytes: 52355}, nil
				},
			}}
		},
	})
	sampler.lastIface[counterBaselineKey("dev-a", "wwan0", trafficCounterSourceQMIWDS)] = trafficCounters{
		RXBytes: 59976,
		TXBytes: 51431,
	}

	sampler.sample(periodStart)

	gotPeriod, rx, tx, err := db.GetLatestMinuteDeltas("iface", "dev-a@wwan0")
	if err != nil {
		t.Fatalf("GetLatestMinuteDeltas() error = %v", err)
	}
	if !gotPeriod.Equal(periodStart) {
		t.Fatalf("period=%s want %s", gotPeriod, periodStart)
	}
	if rx != 1008 || tx != 924 {
		t.Fatalf("rx=%d tx=%d want rx=1008 tx=924", rx, tx)
	}
}

func TestSampleDoesNotFallbackToNetdevAfterWorkerCounterError(t *testing.T) {
	initSamplerTrafficTestDB(t)
	periodStart := time.Date(2026, time.May, 26, 10, 30, 0, 0, time.UTC)
	sampler := New(Options{
		workerInterfaces: func() []workerInterface {
			return []workerInterface{{
				id:     "dev-a",
				iface:  "wwan0",
				source: trafficCounterSourceQMIWDS,
				readCounters: func(context.Context) (trafficCounters, error) {
					return trafficCounters{}, errors.New("wds unavailable")
				},
			}}
		},
	})
	sampler.lastIface[counterBaselineKey("dev-a", "wwan0", trafficCounterSourceQMIWDS)] = trafficCounters{
		RXBytes: 59976,
		TXBytes: 51431,
	}

	sampler.sample(periodStart)

	gotPeriod, rx, tx, err := db.GetLatestMinuteDeltas("iface", "dev-a@wwan0")
	if err != nil {
		t.Fatalf("GetLatestMinuteDeltas() error = %v", err)
	}
	if !gotPeriod.IsZero() || rx != 0 || tx != 0 {
		t.Fatalf("unexpected traffic after QMI WDS error: period=%s rx=%d tx=%d", gotPeriod, rx, tx)
	}
}

func TestSampleSkipsWorkerWithoutQMIWDSReader(t *testing.T) {
	initSamplerTrafficTestDB(t)
	periodStart := time.Date(2026, time.May, 26, 10, 30, 0, 0, time.UTC)
	sampler := New(Options{
		workerInterfaces: func() []workerInterface {
			return []workerInterface{{id: "dev-a", iface: "wwan0"}}
		},
	})
	sampler.lastIface[counterBaselineKey("dev-a", "wwan0", trafficCounterSourceQMIWDS)] = trafficCounters{RXBytes: 1000, TXBytes: 500}

	sampler.sample(periodStart)

	gotPeriod, rx, tx, err := db.GetLatestMinuteDeltas("iface", "dev-a@wwan0")
	if err != nil {
		t.Fatalf("GetLatestMinuteDeltas() error = %v", err)
	}
	if !gotPeriod.IsZero() || rx != 0 || tx != 0 {
		t.Fatalf("unexpected traffic without QMI WDS reader: period=%s rx=%d tx=%d", gotPeriod, rx, tx)
	}
}

func TestSampleSkipsInactiveNetworkAndClearsInterfaceBaseline(t *testing.T) {
	initSamplerTrafficTestDB(t)
	periodStart := time.Date(2026, time.May, 26, 10, 30, 0, 0, time.UTC)
	readCalls := 0
	sampler := New(Options{
		workerInterfaces: func() []workerInterface {
			return []workerInterface{{
				id:     "dev-a",
				iface:  "wwan0",
				source: trafficCounterSourceQMIWDS,
				networkReady: func() bool {
					return false
				},
				readCounters: func(context.Context) (trafficCounters, error) {
					readCalls++
					return trafficCounters{RXBytes: 2000, TXBytes: 1000}, nil
				},
			}}
		},
	})
	key := counterBaselineKey("dev-a", "wwan0", trafficCounterSourceQMIWDS)
	sampler.lastIface[key] = trafficCounters{RXBytes: 1000, TXBytes: 500}

	sampler.sample(periodStart)

	if readCalls != 0 {
		t.Fatalf("readCalls=%d want 0 for inactive network", readCalls)
	}
	if _, ok := sampler.lastIface[key]; ok {
		t.Fatalf("baseline for inactive network was not cleared")
	}
	gotPeriod, rx, tx, err := db.GetLatestMinuteDeltas("iface", "dev-a@wwan0")
	if err != nil {
		t.Fatalf("GetLatestMinuteDeltas() error = %v", err)
	}
	if !gotPeriod.IsZero() || rx != 0 || tx != 0 {
		t.Fatalf("unexpected traffic for inactive network: period=%s rx=%d tx=%d", gotPeriod, rx, tx)
	}
}

func initSamplerTrafficTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "traffic.db")); err != nil {
		t.Fatalf("db.Init() error = %v", err)
	}
	t.Cleanup(func() {
		db.DB = nil
	})
}
