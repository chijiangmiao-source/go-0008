package domain

import "time"

type ReadingDisposition string

const (
	ReadingAccepted  ReadingDisposition = "accepted"
	ReadingDuplicate ReadingDisposition = "duplicate"
	ReadingIsolated  ReadingDisposition = "isolated"
	ReadingRejected  ReadingDisposition = "rejected"
)

type RawReading struct {
	EventID     string    `json:"event_id"`
	ProbeID     string    `json:"probe_id"`
	Sequence    int64     `json:"sequence"`
	DeviceTime  time.Time `json:"device_time"`
	ReceivedAt  time.Time `json:"received_at"`
	Kind        string    `json:"kind,omitempty"`
	Value       float64   `json:"value"`
	Unit        string    `json:"unit"`
	Idempotency string    `json:"idempotency,omitempty"`
}

type SensorReading struct {
	EventID           string             `json:"event_id"`
	ProbeID           string             `json:"probe_id"`
	AreaID            string             `json:"area_id"`
	Sequence          int64              `json:"sequence"`
	DeviceTime        time.Time          `json:"device_time"`
	ReceivedAt        time.Time          `json:"received_at"`
	CorrectedTime     time.Time          `json:"corrected_time"`
	RawValue          float64            `json:"raw_value"`
	RawUnit           string             `json:"raw_unit"`
	Kind              MeasurementKind    `json:"kind"`
	StandardValue     float64            `json:"standard_value"`
	StandardUnit      string             `json:"standard_unit"`
	StandardVersion   string             `json:"standard_version"`
	Disposition       ReadingDisposition `json:"disposition"`
	RejectionReason   string             `json:"rejection_reason,omitempty"`
	BusinessInfluence string             `json:"business_influence,omitempty"`
}

type ReadingResult struct {
	EventID     string             `json:"event_id"`
	ProbeID     string             `json:"probe_id"`
	Sequence    int64              `json:"sequence"`
	Disposition ReadingDisposition `json:"disposition"`
	Code        string             `json:"code"`
	Message     string             `json:"message,omitempty"`
}

type ProbeWindow struct {
	ProbeID       string    `json:"probe_id"`
	AreaID        string    `json:"area_id"`
	LastEventTime time.Time `json:"last_event_time"`
	LastSequence  int64     `json:"last_sequence"`
	Healthy       bool      `json:"healthy"`
}
