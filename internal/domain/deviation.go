package domain

import "time"

type DeviationType string

const (
	DeviationConcentration DeviationType = "concentration_out_of_bounds"
	DeviationProbeLost     DeviationType = "probe_lost"
	DeviationSampleGap     DeviationType = "sample_gap"
	DeviationClockRollback DeviationType = "clock_rollback"
	DeviationCoverage      DeviationType = "coverage_insufficient"
	DeviationControl       DeviationType = "control_abnormal"
)

type DeviationStatus string

const (
	DeviationOpen     DeviationStatus = "open"
	DeviationActioned DeviationStatus = "actioned"
	DeviationReviewed DeviationStatus = "reviewed"
	DeviationClosed   DeviationStatus = "closed"
)

type DeviationSeverity string

const (
	SeverityLow      DeviationSeverity = "low"
	SeverityMedium   DeviationSeverity = "medium"
	SeverityHigh     DeviationSeverity = "high"
	SeverityCritical DeviationSeverity = "critical"
)

type DeviationCase struct {
	ID                 string            `json:"id"`
	Type               DeviationType     `json:"type"`
	Severity           DeviationSeverity `json:"severity"`
	TriggerEvidence    []string          `json:"trigger_evidence"`
	OriginalSafeStatus OperationStatus   `json:"original_safe_status"`
	Status             DeviationStatus   `json:"status"`
	Action             string            `json:"action,omitempty"`
	Review             string            `json:"review,omitempty"`
	Responsible        string            `json:"responsible,omitempty"`
	OpenedAt           time.Time         `json:"opened_at"`
	ClosedAt           *time.Time        `json:"closed_at,omitempty"`
}

func (d DeviationCase) IsOpen() bool { return d.Status != DeviationClosed }

type ResolveDeviationRequest struct {
	Action      string `json:"action"`
	Review      string `json:"review"`
	Responsible string `json:"responsible"`
}
