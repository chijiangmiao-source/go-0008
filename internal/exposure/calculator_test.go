package exposure

import (
	"testing"
	"time"

	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/sensor"
)

func acceptedFor(t *testing.T, at time.Time, minutes int) (domain.FumigationOperation, []domain.SensorReading, domain.Standards) {
	t.Helper()
	req := exposureSampleCreate(at)
	op := domain.FumigationOperation{ID: "op", Organization: req.Organization, Site: req.Site, Carrier: req.Carrier, Agent: req.Agent, DoseGramsPerTonne: req.DoseGramsPerTonne, PlannedSealTime: at, Areas: req.Areas, Coverage: req.Coverage, Probes: req.Probes, Status: domain.StatusExposing}
	standards := domain.Standards{Versions: domain.DefaultStandards(at)}
	snap := domain.OperationSnapshot{Operation: op}
	out, _ := sensor.NewReceiver(standards, 10*time.Minute).Process(nilContext{}, &snap, exposureReadings(at, minutes))
	if len(out.Accepted) == 0 {
		t.Fatal("expected accepted readings")
	}
	return op, snap.Readings, standards
}

type nilContext struct{}

func (nilContext) Deadline() (time.Time, bool)       { return time.Time{}, false }
func (nilContext) Done() <-chan struct{}             { return nil }
func (nilContext) Err() error                        { return nil }
func (nilContext) Value(key interface{}) interface{} { return nil }

type exposureCreate struct {
	Organization      string
	Site              string
	Carrier           domain.CarrierType
	Agent             string
	DoseGramsPerTonne float64
	Areas             []domain.AreaConfig
	Coverage          domain.CoverageRule
	Probes            []domain.ProbeRegistration
}

func exposureSampleCreate(at time.Time) exposureCreate {
	return exposureCreate{
		Organization:      "lab",
		Site:              "bin",
		Carrier:           domain.CarrierSilo,
		Agent:             "phosphine",
		DoseGramsPerTonne: 3.5,
		Coverage:          domain.CoverageRule{MinHealthyPerArea: 1, MaxGap: 20 * time.Minute, LateWindow: 10 * time.Minute},
		Areas:             []domain.AreaConfig{{ID: "north", Name: "north", Grain: "wheat", ProbeIDs: []string{"p-n-1"}, MinHealthy: 1}, {ID: "south", Name: "south", Grain: "wheat", ProbeIDs: []string{"p-s-1"}, MinHealthy: 1}},
		Probes:            []domain.ProbeRegistration{{ID: "p-n-1", AreaID: "north", Range: exposureRange(), Calibration: domain.Calibration{Gain: 1}, EnabledAt: at}, {ID: "p-s-1", AreaID: "south", Range: exposureRange(), Calibration: domain.Calibration{Gain: 1}, EnabledAt: at}},
	}
}

func exposureRange() domain.ProbeRange {
	return domain.ProbeRange{MinConcentration: 0, MaxConcentration: 2000, MinTemperature: -20, MaxTemperature: 80, MinPressure: 80, MaxPressure: 120}
}

func exposureReadings(anchor time.Time, minutes int) []domain.RawReading {
	var readings []domain.RawReading
	for i := 0; i < minutes; i++ {
		t := anchor.Add(time.Duration(i) * time.Minute)
		readings = append(readings,
			domain.RawReading{EventID: "n-c-" + t.Format("150405"), ProbeID: "p-n-1", Sequence: int64(i*4 + 1), DeviceTime: t, ReceivedAt: t, Value: 320, Unit: "ppm"},
			domain.RawReading{EventID: "s-c-" + t.Format("150405"), ProbeID: "p-s-1", Sequence: int64(i*4 + 2), DeviceTime: t, ReceivedAt: t, Value: 320, Unit: "ppm"},
			domain.RawReading{EventID: "n-t-" + t.Format("150405"), ProbeID: "p-n-1", Sequence: int64(i*4 + 3), DeviceTime: t, ReceivedAt: t, Value: 18, Unit: "c"},
			domain.RawReading{EventID: "s-p-" + t.Format("150405"), ProbeID: "p-s-1", Sequence: int64(i*4 + 4), DeviceTime: t, ReceivedAt: t, Value: 101, Unit: "kpa"},
		)
	}
	return readings
}

func TestCalculatorAccumulatesValidExposure(t *testing.T) {
	at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	op, readings, standards := acceptedFor(t, at, 95)
	ledger, deviations := NewCalculator(standards).Recalculate(op, readings)
	if ledger.AccumulatedValid < 90*time.Minute || ledger.RequirementSatisfiedAt == nil || len(deviations) != 0 {
		t.Fatalf("unexpected ledger: valid=%s satisfied=%v deviations=%d", ledger.AccumulatedValid, ledger.RequirementSatisfiedAt, len(deviations))
	}
}

func TestCalculatorExcludesSampleGapBeyondTolerance(t *testing.T) {
	at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	op, readings, standards := acceptedFor(t, at, 2)
	for i := range readings {
		if readings[i].CorrectedTime.After(at) {
			readings[i].CorrectedTime = readings[i].CorrectedTime.Add(30 * time.Minute)
		}
	}
	ledger, deviations := NewCalculator(standards).Recalculate(op, readings)
	if ledger.ExcludedSegments() == 0 || len(deviations) == 0 {
		t.Fatalf("gap was not excluded: segments=%#v deviations=%#v", ledger.Segments, deviations)
	}
}

func TestCalculatorSplitsByEffectiveStandardVersion(t *testing.T) {
	at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	op, readings, standards := acceptedFor(t, at, 50)
	ledger, _ := NewCalculator(standards).Recalculate(op, readings)
	seen := map[string]bool{}
	for _, seg := range ledger.Segments {
		seen[seg.StandardVersion] = true
	}
	if !seen["CN-GRAIN-SAMPLE-2026-A"] || !seen["CN-GRAIN-SAMPLE-2026-B"] {
		t.Fatalf("expected both standard versions, saw %#v", seen)
	}
}
