package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

type ArchiveManifest struct {
	ID                 string            `json:"id"`
	OperationID        string            `json:"operation_id"`
	CreatedAt          time.Time         `json:"created_at"`
	ConfigDigest       string            `json:"config_digest"`
	StandardDigests    map[string]string `json:"standard_digests"`
	RecordCounts       map[string]int    `json:"record_counts"`
	IntegrityDigest    string            `json:"integrity_digest"`
	IncludesEntryAck   bool              `json:"includes_entry_ack"`
	IncludesLedger     bool              `json:"includes_ledger"`
	IncludesDeviations bool              `json:"includes_deviations"`
}

func (m *ArchiveManifest) Seal() error {
	cp := *m
	cp.IntegrityDigest = ""
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	m.IntegrityDigest = hex.EncodeToString(sum[:])
	return nil
}

func (m ArchiveManifest) Verify() bool {
	cp := m
	want := cp.IntegrityDigest
	cp.IntegrityDigest = ""
	data, err := json.Marshal(cp)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == want
}
