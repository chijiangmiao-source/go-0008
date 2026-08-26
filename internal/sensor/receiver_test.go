package sensor

import (
	"context"
	"testing"
	"time"

	"grain-fumigation-interlock/internal/domain"
)

func receiverSnapshot(at time.Time) domain.OperationSnapshot {
	req := sampleCreate(at)
	op := domain.FumigationOperation{
		ID: "op", Organization: req.Organization, Site: req.Site, Carrier: req.Carrier, Agent: req.Agent,
		DoseGramsPerTonne: req.DoseGramsPerTonne, PlannedSealTime: at, Areas: req.Areas, Coverage: req.Coverage,
		Probes: req.Probes, Status: domain.StatusExposing, StandardVersion: "CN-GRAIN-SAMPLE-2026-A", Revision: 2,
	}
	return domain.OperationSnapshot{Operation: op, Ledger: domain.ExposureLedger{LastCoverageByArea: map[string]int{}}}
}

type sampleCreateRequest struct {
	Organization      string
	Site              string
	Carrier           domain.CarrierType
	Agent             string
	DoseGramsPerTonne float64
	Areas             []domain.AreaConfig
	Coverage          domain.CoverageRule
	Probes            []domain.ProbeRegistration
}

func sampleCreate(at time.Time) sampleCreateRequest {
	return sampleCreateRequest{
		Organization:      "lab",
		Site:              "bin",
		Carrier:           domain.CarrierSilo,
		Agent:             "phosphine",
		DoseGramsPerTonne: 3.5,
		Coverage:          domain.CoverageRule{MinHealthyPerArea: 1, MaxGap: 20 * time.Minute, LateWindow: 10 * time.Minute},
		Areas:             []domain.AreaConfig{{ID: "north", Name: "north", Grain: "wheat", ProbeIDs: []string{"p-n-1"}, MinHealthy: 1}, {ID: "south", Name: "south", Grain: "wheat", ProbeIDs: []string{"p-s-1"}, MinHealthy: 1}},
		Probes:            []domain.ProbeRegistration{{ID: "p-n-1", AreaID: "north", Range: sampleRange(), Calibration: domain.Calibration{Gain: 1}, EnabledAt: at}, {ID: "p-s-1", AreaID: "south", Range: sampleRange(), Calibration: domain.Calibration{Gain: 1}, EnabledAt: at}},
	}
}

func sampleRange() domain.ProbeRange {
	return domain.ProbeRange{MinConcentration: 0, MaxConcentration: 2000, MinTemperature: -20, MaxTemperature: 80, MinPressure: 80, MaxPressure: 120}
}

func TestReceiverDeduplicatesBusinessImpact(t *testing.T) {
	at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	standards := domain.Standards{Versions: domain.DefaultStandards(at)}
	receiver := NewReceiver(standards, 10*time.Minute)
	snap := receiverSnapshot(at)
	raw := domain.RawReading{EventID: "e1", ProbeID: "p-n-1", Sequence: 1, DeviceTime: at, ReceivedAt: at.Add(time.Second), Value: 320, Unit: "ppm"}
	first, _ := receiver.Process(context.Background(), &snap, []domain.RawReading{raw})
	second, _ := receiver.Process(context.Background(), &snap, []domain.RawReading{raw})
	if first.Results[0].Disposition != domain.ReadingAccepted || second.Results[0].Disposition != domain.ReadingDuplicate {
		t.Fatalf("unexpected dispositions: %#v %#v", first.Results, second.Results)
	}
	if len(snap.Readings) != 1 {
		t.Fatalf("duplicate changed readings: %d", len(snap.Readings))
	}
}

func TestReceiverRejectsUnknownUnitPerEvent(t *testing.T) {
	at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	receiver := NewReceiver(domain.Standards{Versions: domain.DefaultStandards(at)}, 10*time.Minute)
	snap := receiverSnapshot(at)
	raws := []domain.RawReading{
		{EventID: "bad", ProbeID: "p-n-1", Sequence: 1, DeviceTime: at, ReceivedAt: at, Value: 1, Unit: "mystery"},
		{EventID: "good", ProbeID: "p-s-1", Sequence: 1, DeviceTime: at, ReceivedAt: at, Value: 320, Unit: "ppm"},
	}
	out, _ := receiver.Process(context.Background(), &snap, raws)
	if out.Results[0].Disposition != domain.ReadingRejected || out.Results[1].Disposition != domain.ReadingAccepted {
		t.Fatalf("bad event affected valid event: %#v", out.Results)
	}
}

func TestReceiverIsolatesClockRollback(t *testing.T) {
	at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	receiver := NewReceiver(domain.Standards{Versions: domain.DefaultStandards(at)}, 10*time.Minute)
	snap := receiverSnapshot(at)
	raws := []domain.RawReading{
		{EventID: "new", ProbeID: "p-n-1", Sequence: 10, DeviceTime: at.Add(10 * time.Minute), ReceivedAt: at.Add(10 * time.Minute), Value: 320, Unit: "ppm"},
		{EventID: "old", ProbeID: "p-n-1", Sequence: 11, DeviceTime: at.Add(5 * time.Minute), ReceivedAt: at.Add(11 * time.Minute), Value: 320, Unit: "ppm"},
	}
	out, deviations := receiver.Process(context.Background(), &snap, raws)
	if out.Results[1].Disposition != domain.ReadingIsolated || len(deviations) == 0 {
		t.Fatalf("rollback was not isolated: %#v deviations=%#v", out.Results, deviations)
	}
}
