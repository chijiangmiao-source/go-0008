package replay

import (
	"time"

	"grain-fumigation-interlock/internal/app"
	"grain-fumigation-interlock/internal/domain"
)

func SampleCreate(anchor time.Time) app.CreateOperationRequest {
	return app.CreateOperationRequest{
		Organization:      "third-party-inspection-lab",
		Site:              "plant-a-bin-7",
		Carrier:           domain.CarrierSilo,
		Agent:             "phosphine",
		DoseGramsPerTonne: 3.5,
		PlannedSealTime:   anchor,
		Coverage:          domain.CoverageRule{MinHealthyPerArea: 1, MaxGap: 20 * time.Minute, LateWindow: 10 * time.Minute},
		Areas: []domain.AreaConfig{
			{ID: "north", Name: "north stack", Grain: "wheat", ProbeIDs: []string{"p-n-1"}, MinHealthy: 1},
			{ID: "south", Name: "south stack", Grain: "wheat", ProbeIDs: []string{"p-s-1"}, MinHealthy: 1},
		},
		Probes: []domain.ProbeRegistration{
			{ID: "p-n-1", AreaID: "north", Range: SampleRange(), Calibration: domain.Calibration{Gain: 1}, EnabledAt: anchor},
			{ID: "p-s-1", AreaID: "south", Range: SampleRange(), Calibration: domain.Calibration{Gain: 1}, EnabledAt: anchor},
		},
	}
}

func SampleRange() domain.ProbeRange {
	return domain.ProbeRange{MinConcentration: 0, MaxConcentration: 2000, MinTemperature: -20, MaxTemperature: 80, MinPressure: 80, MaxPressure: 120}
}

func ExposureReadings(anchor time.Time, minutes int, residual bool) []domain.RawReading {
	var readings []domain.RawReading
	value := 320.0
	if residual {
		value = 1.2
	}
	for i := 0; i < minutes; i++ {
		t := anchor.Add(time.Duration(i) * time.Minute)
		readings = append(readings,
			domain.RawReading{EventID: "n-c-" + t.Format("150405"), ProbeID: "p-n-1", Sequence: int64(i*3 + 1), DeviceTime: t, ReceivedAt: t.Add(time.Second), Value: value, Unit: "ppm"},
			domain.RawReading{EventID: "s-c-" + t.Format("150405"), ProbeID: "p-s-1", Sequence: int64(i*3 + 2), DeviceTime: t, ReceivedAt: t.Add(time.Second), Value: value, Unit: "ppm"},
			domain.RawReading{EventID: "n-t-" + t.Format("150405"), ProbeID: "p-n-1", Sequence: int64(i*3 + 3), DeviceTime: t, ReceivedAt: t.Add(time.Second), Value: 18, Unit: "c"},
			domain.RawReading{EventID: "s-p-" + t.Format("150405"), ProbeID: "p-s-1", Sequence: int64(i*3 + 4), DeviceTime: t, ReceivedAt: t.Add(time.Second), Value: 101, Unit: "kpa"},
		)
	}
	return readings
}
