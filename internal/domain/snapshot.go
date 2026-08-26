package domain

import "time"

type OperationSnapshot struct {
	Operation   FumigationOperation  `json:"operation"`
	Ledger      ExposureLedger       `json:"ledger"`
	Readings    []SensorReading      `json:"readings"`
	Deviations  []DeviationCase      `json:"deviations"`
	Ventilation VentilationExecution `json:"ventilation"`
	Entry       EntryPermit          `json:"entry"`
	Archive     *ArchiveManifest     `json:"archive,omitempty"`
	Events      []EventRecord        `json:"events,omitempty"`
}

type EventRecord struct {
	Sequence    int64       `json:"sequence"`
	AggregateID string      `json:"aggregate_id"`
	Version     int64       `json:"version"`
	Type        string      `json:"type"`
	Payload     interface{} `json:"payload"`
	OccurredAt  time.Time   `json:"occurred_at"`
}

type EvidencePage struct {
	Items      []interface{} `json:"items"`
	NextOffset int           `json:"next_offset"`
	Total      int           `json:"total"`
}

func (s OperationSnapshot) OpenDeviationCount() int {
	n := 0
	for _, d := range s.Deviations {
		if d.IsOpen() {
			n++
		}
	}
	return n
}

func (s OperationSnapshot) LastAcceptedReading() *SensorReading {
	for i := len(s.Readings) - 1; i >= 0; i-- {
		if s.Readings[i].Disposition == ReadingAccepted {
			return &s.Readings[i]
		}
	}
	return nil
}
