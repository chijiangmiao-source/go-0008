package exposure

import (
	"sort"
	"time"

	"grain-fumigation-interlock/internal/domain"
)

type Calculator struct {
	Standards domain.Standards
}

func NewCalculator(standards domain.Standards) Calculator {
	return Calculator{Standards: standards}
}

func (c Calculator) Recalculate(operation domain.FumigationOperation, readings []domain.SensorReading) (domain.ExposureLedger, []domain.DeviationCase) {
	accepted := acceptedReadings(readings)
	sort.Slice(accepted, func(i, j int) bool {
		if accepted[i].CorrectedTime.Equal(accepted[j].CorrectedTime) {
			return accepted[i].EventID < accepted[j].EventID
		}
		return accepted[i].CorrectedTime.Before(accepted[j].CorrectedTime)
	})
	ledger := domain.ExposureLedger{LastCoverageByArea: map[string]int{}}
	var deviations []domain.DeviationCase
	if len(accepted) == 0 {
		return ledger, deviations
	}
	buckets := bucketByMinute(accepted)
	times := make([]time.Time, 0, len(buckets))
	for t := range buckets {
		times = append(times, t)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	for idx, start := range times {
		standard, err := c.Standards.ForEventTime(start)
		if err != nil {
			seg := excludedSegment(start, start.Add(time.Minute), buckets[start], "standard unavailable")
			ledger.Append(seg, time.Hour)
			continue
		}
		end := start.Add(time.Minute)
		if idx+1 < len(times) {
			next := times[idx+1]
			if next.Sub(start) <= standard.MaxSampleGap {
				end = next
			} else {
				gapSeg := excludedSegment(start, next, buckets[start], "sample gap exceeds tolerance")
				ledger.Append(gapSeg, standard.RequiredDuration)
				deviations = append(deviations, deviation(domain.DeviationSampleGap, domain.SeverityMedium, operation.Status, "gap", next))
				continue
			}
		}
		seg := c.evaluateSegment(operation, standard, start, end, buckets[start])
		ledger.Append(seg, standard.RequiredDuration)
		if !seg.Included && seg.Reason == "coverage insufficient" {
			deviations = append(deviations, deviation(domain.DeviationCoverage, domain.SeverityCritical, operation.Status, "coverage", end))
		}
	}
	return ledger, deviations
}

func (c Calculator) evaluateSegment(operation domain.FumigationOperation, standard domain.SafetyStandardVersion, start, end time.Time, readings []domain.SensorReading) domain.ExposureSegment {
	coverage := map[string]int{}
	seenProbe := map[string]bool{}
	var concentration, temperature, pressure sampleMean
	var events []string
	for _, r := range readings {
		if !seenProbe[r.ProbeID] {
			coverage[r.AreaID]++
			seenProbe[r.ProbeID] = true
		}
		events = append(events, r.EventID)
		switch r.Kind {
		case domain.MeasureConcentration:
			concentration.add(r.StandardValue)
		case domain.MeasureTemperature:
			temperature.add(r.StandardValue)
		case domain.MeasurePressure:
			pressure.add(r.StandardValue)
		}
	}
	included := true
	reason := "all registered areas covered and thresholds satisfied"
	for _, area := range operation.Areas {
		min := area.MinHealthy
		if min <= 0 {
			min = operation.Coverage.MinHealthyPerArea
		}
		if coverage[area.ID] < min {
			included = false
			reason = "coverage insufficient"
			break
		}
	}
	if included && concentration.count == 0 {
		included = false
		reason = "concentration missing"
	}
	if included && concentration.mean() < standard.MinConcentrationPPM {
		included = false
		reason = "concentration below standard"
	}
	if included && temperature.count > 0 && temperature.mean() < standard.MinTemperatureC {
		included = false
		reason = "temperature below standard"
	}
	if included && pressure.count > 0 && (pressure.mean() < standard.PressureMinKPA || pressure.mean() > standard.PressureMaxKPA) {
		included = false
		reason = "pressure outside standard"
	}
	return domain.ExposureSegment{
		Start: start, End: end, AreaEvidence: coverage, StandardVersion: standard.Version, Included: included, Reason: reason,
		MeanConcentration: concentration.mean(), MeanTemperature: temperature.mean(), MeanPressure: pressure.mean(),
		ContributingEvents: events, Disposition: domain.ReadingAccepted,
	}
}

func acceptedReadings(readings []domain.SensorReading) []domain.SensorReading {
	out := make([]domain.SensorReading, 0, len(readings))
	for _, r := range readings {
		if r.Disposition == domain.ReadingAccepted {
			out = append(out, r)
		}
	}
	return out
}

func bucketByMinute(readings []domain.SensorReading) map[time.Time][]domain.SensorReading {
	buckets := map[time.Time][]domain.SensorReading{}
	for _, r := range readings {
		t := r.CorrectedTime.Truncate(time.Minute)
		buckets[t] = append(buckets[t], r)
	}
	return buckets
}

func excludedSegment(start, end time.Time, readings []domain.SensorReading, reason string) domain.ExposureSegment {
	events := make([]string, 0, len(readings))
	coverage := map[string]int{}
	for _, r := range readings {
		events = append(events, r.EventID)
		coverage[r.AreaID]++
	}
	return domain.ExposureSegment{Start: start, End: end, AreaEvidence: coverage, Included: false, Reason: reason, ContributingEvents: events}
}

type sampleMean struct {
	total float64
	count int
}

func (m *sampleMean) add(v float64) {
	m.total += v
	m.count++
}

func (m sampleMean) mean() float64 {
	if m.count == 0 {
		return 0
	}
	return m.total / float64(m.count)
}

func deviation(kind domain.DeviationType, severity domain.DeviationSeverity, state domain.OperationStatus, evidence string, at time.Time) domain.DeviationCase {
	return domain.DeviationCase{ID: string(kind) + "-" + at.UTC().Format("20060102150405"), Type: kind, Severity: severity, OriginalSafeStatus: state, Status: domain.DeviationOpen, TriggerEvidence: []string{evidence}, OpenedAt: at.UTC()}
}
