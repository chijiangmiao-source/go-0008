package sensor

import (
	"context"
	"sort"
	"strings"
	"time"

	"grain-fumigation-interlock/internal/domain"
)

type Receiver struct {
	Calibrator Calibrator
	Standards  domain.Standards
}

type BatchOutput struct {
	Accepted []domain.SensorReading `json:"accepted"`
	Results  []domain.ReadingResult `json:"results"`
}

func NewReceiver(standards domain.Standards, lateWindow time.Duration) Receiver {
	return Receiver{Calibrator: NewCalibrator(lateWindow * 3), Standards: standards}
}

func (r Receiver) Process(ctx context.Context, snap *domain.OperationSnapshot, raws []domain.RawReading) (BatchOutput, []domain.DeviationCase) {
	output := BatchOutput{Results: make([]domain.ReadingResult, 0, len(raws))}
	var deviations []domain.DeviationCase
	seen := existingReadingKeys(snap.Readings)
	for _, raw := range raws {
		if err := ctx.Err(); err != nil {
			output.Results = append(output.Results, resultFor(raw, domain.ReadingRejected, "context_canceled", err.Error()))
			continue
		}
		key := readingKey(raw.ProbeID, raw.EventID)
		if seen[key] {
			output.Results = append(output.Results, resultFor(raw, domain.ReadingDuplicate, "duplicate_event", "reading was already applied"))
			continue
		}
		probe, ok := snap.Operation.ProbeByID(raw.ProbeID)
		if !ok {
			output.Results = append(output.Results, resultFor(raw, domain.ReadingRejected, "probe_unknown", "probe is not registered for operation"))
			continue
		}
		if probe.LastCorrectedAt != nil && raw.DeviceTime.Add(probe.Calibration.ClockOffset).Before(*probe.LastCorrectedAt) {
			isolated := isolatedReading(raw, probe, "clock_rollback")
			snap.Readings = append(snap.Readings, isolated)
			snap.Operation.SetProbeHealth(probe.ID, domain.ProbeIsolated, "clock rollback", raw.EventID, raw.Sequence, isolated.CorrectedTime)
			deviations = append(deviations, NewDeviation(domain.DeviationClockRollback, domain.SeverityHigh, snap.Operation.Status, raw.EventID, raw.ReceivedAt))
			output.Results = append(output.Results, resultFor(raw, domain.ReadingIsolated, "clock_rollback", "device clock moved backward"))
			seen[key] = true
			continue
		}
		standard, err := r.Standards.ForEventTime(raw.DeviceTime.Add(probe.Calibration.ClockOffset))
		if err != nil {
			output.Results = append(output.Results, resultFor(raw, domain.ReadingRejected, "standard_unavailable", err.Error()))
			continue
		}
		reading, err := r.Calibrator.Correct(raw, probe, standard)
		if err != nil {
			if reading.Disposition == "" {
				reading.Disposition = domain.ReadingRejected
			}
			output.Results = append(output.Results, resultFor(raw, reading.Disposition, boundaryCode(err), err.Error()))
			continue
		}
		if tooLate(reading, snap.Ledger.Watermark, standard.LateWindow) {
			reading.Disposition = domain.ReadingIsolated
			reading.RejectionReason = "older than ledger watermark and late window"
			snap.Readings = append(snap.Readings, reading)
			output.Results = append(output.Results, resultFor(raw, domain.ReadingIsolated, "late_beyond_watermark", reading.RejectionReason))
			seen[key] = true
			continue
		}
		if violatesEnvironment(reading, standard) {
			reading.Disposition = domain.ReadingIsolated
			reading.RejectionReason = "standard threshold violated"
			snap.Readings = append(snap.Readings, reading)
			snap.Operation.SetProbeHealth(probe.ID, domain.ProbeIsolated, "threshold violation", raw.EventID, raw.Sequence, reading.CorrectedTime)
			deviations = append(deviations, NewDeviation(domain.DeviationConcentration, domain.SeverityHigh, snap.Operation.Status, raw.EventID, raw.ReceivedAt))
			output.Results = append(output.Results, resultFor(raw, domain.ReadingIsolated, "threshold_violation", reading.RejectionReason))
			seen[key] = true
			continue
		}
		reading.BusinessInfluence = "eligible_for_exposure_recalculation"
		snap.Readings = append(snap.Readings, reading)
		snap.Operation.SetProbeHealth(probe.ID, domain.ProbeHealthy, "", raw.EventID, raw.Sequence, reading.CorrectedTime)
		output.Accepted = append(output.Accepted, reading)
		output.Results = append(output.Results, resultFor(raw, domain.ReadingAccepted, "accepted", ""))
		seen[key] = true
	}
	sort.Slice(snap.Readings, func(i, j int) bool {
		if snap.Readings[i].CorrectedTime.Equal(snap.Readings[j].CorrectedTime) {
			return snap.Readings[i].EventID < snap.Readings[j].EventID
		}
		return snap.Readings[i].CorrectedTime.Before(snap.Readings[j].CorrectedTime)
	})
	if !snap.Operation.CoverageSatisfied() {
		deviations = append(deviations, NewDeviation(domain.DeviationCoverage, domain.SeverityCritical, snap.Operation.Status, "coverage", time.Now().UTC()))
	}
	return output, deviations
}

func existingReadingKeys(readings []domain.SensorReading) map[string]bool {
	seen := map[string]bool{}
	for _, r := range readings {
		seen[readingKey(r.ProbeID, r.EventID)] = true
	}
	return seen
}

func readingKey(probeID, eventID string) string {
	return probeID + ":" + eventID
}

func resultFor(raw domain.RawReading, disposition domain.ReadingDisposition, code, message string) domain.ReadingResult {
	return domain.ReadingResult{EventID: raw.EventID, ProbeID: raw.ProbeID, Sequence: raw.Sequence, Disposition: disposition, Code: code, Message: message}
}

func isolatedReading(raw domain.RawReading, probe domain.ProbeRegistration, reason string) domain.SensorReading {
	return domain.SensorReading{
		EventID: raw.EventID, ProbeID: raw.ProbeID, AreaID: probe.AreaID, Sequence: raw.Sequence,
		DeviceTime: raw.DeviceTime.UTC(), ReceivedAt: raw.ReceivedAt.UTC(), CorrectedTime: raw.DeviceTime.Add(probe.Calibration.ClockOffset).UTC(),
		RawValue: raw.Value, RawUnit: raw.Unit, Disposition: domain.ReadingIsolated, RejectionReason: reason,
	}
}

func tooLate(reading domain.SensorReading, watermark time.Time, lateWindow time.Duration) bool {
	return !watermark.IsZero() && reading.CorrectedTime.Before(watermark.Add(-lateWindow))
}

func violatesEnvironment(reading domain.SensorReading, standard domain.SafetyStandardVersion) bool {
	switch reading.Kind {
	case domain.MeasureConcentration:
		return reading.StandardValue < standard.MinConcentrationPPM
	case domain.MeasureTemperature:
		return reading.StandardValue < standard.MinTemperatureC
	case domain.MeasurePressure:
		return reading.StandardValue < standard.PressureMinKPA || reading.StandardValue > standard.PressureMaxKPA
	default:
		return true
	}
}

func boundaryCode(err error) string {
	if b, ok := err.(domain.BoundaryError); ok {
		return b.Code
	}
	text := strings.ReplaceAll(err.Error(), " ", "_")
	if len(text) > 48 {
		text = text[:48]
	}
	return text
}

func NewDeviation(kind domain.DeviationType, severity domain.DeviationSeverity, status domain.OperationStatus, evidence string, at time.Time) domain.DeviationCase {
	return domain.DeviationCase{
		ID:                 string(kind) + "-" + evidence,
		Type:               kind,
		Severity:           severity,
		TriggerEvidence:    []string{evidence},
		OriginalSafeStatus: status,
		Status:             domain.DeviationOpen,
		OpenedAt:           at.UTC(),
	}
}
