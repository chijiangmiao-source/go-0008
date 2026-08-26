package ventilation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/store"
)

type EntryService struct {
	Controller VentilationControllerPort
	Store      store.EventStorePort
}

type EntryRequest struct {
	RequestID   string `json:"request_id"`
	Operator    string `json:"operator"`
	Idempotency string `json:"idempotency"`
}

func (e EntryService) Request(ctx context.Context, snapshot *domain.OperationSnapshot, req EntryRequest, now time.Time) (domain.EntryPermit, error) {
	if req.RequestID == "" || req.Idempotency == "" {
		return domain.EntryPermit{}, domain.ValidationError{Code: "entry_request_incomplete", Message: "request_id and idempotency are required"}
	}
	if snapshot.Entry.CommittedAt != nil {
		return snapshot.Entry, nil
	}
	if snapshot.Operation.Status != domain.StatusVentilating || snapshot.Ventilation.NextStage() != domain.StageComplete {
		return domain.EntryPermit{}, domain.ConflictError{Code: "ventilation_incomplete", Message: "all ventilation stages must be acked"}
	}
	if snapshot.OpenDeviationCount() > 0 {
		return domain.EntryPermit{}, domain.ConflictError{Code: "open_deviation_blocks_entry", Message: "entry remains locked"}
	}
	last := snapshot.LastAcceptedReading()
	if last == nil {
		return domain.EntryPermit{}, domain.ConflictError{Code: "fresh_reading_required", Message: "entry requires accepted residual reading"}
	}
	if last.Kind == domain.MeasureConcentration && last.StandardValue > 2.0 {
		return domain.EntryPermit{}, domain.ConflictError{Code: "residual_too_high", Message: "entry remains locked"}
	}
	condition := conditionDigest(snapshot, last)
	commandID := snapshot.Operation.ID + "-entry-" + req.Idempotency
	intent := domain.ControlIntent{CommandID: commandID, OperationID: snapshot.Operation.ID, Stage: domain.StageComplete, Idempotency: req.Idempotency, CreatedAt: now.UTC(), Status: domain.CommandPending}
	if e.Store != nil {
		if err := e.Store.AppendOutbox(ctx, intent); err != nil {
			return domain.EntryPermit{}, err
		}
	}
	ack, err := e.Controller.Send(ctx, intent)
	if err != nil {
		return domain.EntryPermit{}, err
	}
	if e.Store != nil {
		if err := e.Store.MarkOutbox(ctx, commandID, ack); err != nil {
			return domain.EntryPermit{}, err
		}
	}
	if !ack.Accepted {
		return domain.EntryPermit{}, domain.BoundaryError{Boundary: domain.BoundaryControl, Code: "unlock_rejected", Message: ack.Message}
	}
	committed := ack.ReceivedAt
	permit := domain.EntryPermit{RequestID: req.RequestID, ConditionSnapshot: condition, UnlockCommandID: commandID, Ack: &ack, CommittedAt: &committed}
	snapshot.Entry = permit
	snapshot.Operation.Status = domain.StatusEntryUnlocked
	if strings.TrimSpace(req.Operator) != "" {
		snapshot.Operation.LastAuditMessage = "entry unlocked by " + req.Operator
	}
	return permit, nil
}

func conditionDigest(snapshot *domain.OperationSnapshot, last *domain.SensorReading) string {
	payload := map[string]interface{}{
		"operation":  snapshot.Operation.ID,
		"revision":   snapshot.Operation.Revision,
		"stage":      snapshot.Ventilation.CompletedStages,
		"reading":    last.EventID,
		"deviations": snapshot.OpenDeviationCount(),
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
