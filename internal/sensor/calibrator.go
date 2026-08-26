package sensor

import (
	"math"
	"time"

	"grain-fumigation-interlock/internal/domain"
)

type Calibrator struct {
	MaxClockSkew time.Duration
}

func NewCalibrator(maxSkew time.Duration) Calibrator {
	if maxSkew <= 0 {
		maxSkew = 30 * time.Minute
	}
	return Calibrator{MaxClockSkew: maxSkew}
}

func (c Calibrator) Correct(raw domain.RawReading, probe domain.ProbeRegistration, standard domain.SafetyStandardVersion) (domain.SensorReading, error) {
	if raw.EventID == "" || raw.ProbeID == "" {
		return domain.SensorReading{}, domain.BoundaryError{Boundary: domain.BoundaryInput, Code: "reading_identity_missing", Message: "event_id and probe_id are required"}
	}
	if raw.Sequence <= 0 {
		return domain.SensorReading{}, domain.BoundaryError{Boundary: domain.BoundaryInput, Code: "sequence_invalid", Message: "sequence must be positive"}
	}
	if math.IsNaN(raw.Value) || math.IsInf(raw.Value, 0) {
		return domain.SensorReading{}, domain.BoundaryError{Boundary: domain.BoundaryInput, Code: "value_invalid", Message: "value must be finite"}
	}
	if raw.DeviceTime.IsZero() {
		return domain.SensorReading{}, domain.BoundaryError{Boundary: domain.BoundaryTime, Code: "device_time_missing", Message: "device time is required"}
	}
	if raw.ReceivedAt.IsZero() {
		raw.ReceivedAt = time.Now().UTC()
	}
	if raw.ReceivedAt.Sub(raw.DeviceTime) > c.MaxClockSkew || raw.DeviceTime.Sub(raw.ReceivedAt) > c.MaxClockSkew {
		return domain.SensorReading{}, domain.BoundaryError{Boundary: domain.BoundaryTime, Code: "clock_skew_excessive", Message: "device time outside correction window"}
	}
	kind, converted, stdUnit, err := standard.Convert(raw.Unit, raw.Value)
	if err != nil {
		return domain.SensorReading{}, err
	}
	gain := probe.Calibration.Gain
	if gain == 0 {
		gain = 1
	}
	converted = converted*gain + probe.Calibration.Bias
	corrected := raw.DeviceTime.Add(probe.Calibration.ClockOffset).UTC()
	reading := domain.SensorReading{
		EventID:         raw.EventID,
		ProbeID:         raw.ProbeID,
		AreaID:          probe.AreaID,
		Sequence:        raw.Sequence,
		DeviceTime:      raw.DeviceTime.UTC(),
		ReceivedAt:      raw.ReceivedAt.UTC(),
		CorrectedTime:   corrected,
		RawValue:        raw.Value,
		RawUnit:         raw.Unit,
		Kind:            kind,
		StandardValue:   converted,
		StandardUnit:    stdUnit,
		StandardVersion: standard.Version,
		Disposition:     domain.ReadingAccepted,
	}
	if err := c.validateRange(reading, probe); err != nil {
		reading.Disposition = domain.ReadingRejected
		reading.RejectionReason = err.Error()
		return reading, err
	}
	return reading, nil
}

func (c Calibrator) validateRange(reading domain.SensorReading, probe domain.ProbeRegistration) error {
	switch reading.Kind {
	case domain.MeasureConcentration:
		if reading.StandardValue < probe.Range.MinConcentration || reading.StandardValue > probe.Range.MaxConcentration {
			return domain.BoundaryError{Boundary: domain.BoundaryInput, Code: "concentration_range", Message: "concentration outside probe range"}
		}
	case domain.MeasureTemperature:
		if reading.StandardValue < probe.Range.MinTemperature || reading.StandardValue > probe.Range.MaxTemperature {
			return domain.BoundaryError{Boundary: domain.BoundaryInput, Code: "temperature_range", Message: "temperature outside probe range"}
		}
	case domain.MeasurePressure:
		if reading.StandardValue < probe.Range.MinPressure || reading.StandardValue > probe.Range.MaxPressure {
			return domain.BoundaryError{Boundary: domain.BoundaryInput, Code: "pressure_range", Message: "pressure outside probe range"}
		}
	default:
		return domain.BoundaryError{Boundary: domain.BoundaryInput, Code: "measurement_kind_unknown", Message: string(reading.Kind)}
	}
	return nil
}
