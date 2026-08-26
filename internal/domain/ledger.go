package domain

import "time"

type ExposureLedger struct {
	Watermark              time.Time         `json:"watermark"`
	AccumulatedValid       time.Duration     `json:"accumulated_valid"`
	AccumulatedPPMMinutes  float64           `json:"accumulated_ppm_minutes"`
	Segments               []ExposureSegment `json:"segments"`
	LastCoverageByArea     map[string]int    `json:"last_coverage_by_area"`
	LastExcludedReason     string            `json:"last_excluded_reason,omitempty"`
	RequirementSatisfiedAt *time.Time        `json:"requirement_satisfied_at,omitempty"`
}

type ExposureSegment struct {
	Start              time.Time          `json:"start"`
	End                time.Time          `json:"end"`
	AreaEvidence       map[string]int     `json:"area_evidence"`
	StandardVersion    string             `json:"standard_version"`
	Included           bool               `json:"included"`
	Reason             string             `json:"reason"`
	MeanConcentration  float64            `json:"mean_concentration_ppm"`
	MeanTemperature    float64            `json:"mean_temperature_c"`
	MeanPressure       float64            `json:"mean_pressure_kpa"`
	ContributingEvents []string           `json:"contributing_events"`
	Disposition        ReadingDisposition `json:"disposition"`
}

func (l ExposureLedger) Remaining(required time.Duration) time.Duration {
	if l.AccumulatedValid >= required {
		return 0
	}
	return required - l.AccumulatedValid
}

func (l ExposureLedger) IncludedSegments() int {
	n := 0
	for _, s := range l.Segments {
		if s.Included {
			n++
		}
	}
	return n
}

func (l ExposureLedger) ExcludedSegments() int {
	n := 0
	for _, s := range l.Segments {
		if !s.Included {
			n++
		}
	}
	return n
}

func (l *ExposureLedger) Append(seg ExposureSegment, required time.Duration) {
	l.Segments = append(l.Segments, seg)
	if seg.End.After(l.Watermark) {
		l.Watermark = seg.End
	}
	l.LastCoverageByArea = seg.AreaEvidence
	if seg.Included {
		dur := seg.End.Sub(seg.Start)
		l.AccumulatedValid += dur
		l.AccumulatedPPMMinutes += seg.MeanConcentration * dur.Minutes()
		if l.AccumulatedValid >= required && l.RequirementSatisfiedAt == nil {
			t := seg.End
			l.RequirementSatisfiedAt = &t
		}
	} else {
		l.LastExcludedReason = seg.Reason
	}
}
