package ventilation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/store"
)

type Interlock struct {
	Controller VentilationControllerPort
	Store      store.EventStorePort
}

type CommandRequest struct {
	Idempotency      string                  `json:"idempotency"`
	ExpectedStage    domain.VentilationStage `json:"expected_stage"`
	Stage            domain.VentilationStage `json:"stage"`
	ExpectedRevision int64                   `json:"expected_revision"`
}

func (i Interlock) StartOrAdvance(ctx context.Context, snap *domain.OperationSnapshot, req CommandRequest, now time.Time) (domain.ControlAck, domain.ControlIntent, error) {
	if strings.TrimSpace(req.Idempotency) == "" {
		return domain.ControlAck{}, domain.ControlIntent{}, domain.ValidationError{Code: "idempotency_required", Message: "control idempotency is required"}
	}
	if snap.Operation.Status != domain.StatusReadyToVent && snap.Operation.Status != domain.StatusVentilating {
		return domain.ControlAck{}, domain.ControlIntent{}, domain.ConflictError{Code: "operation_not_ready_for_ventilation", Message: string(snap.Operation.Status)}
	}
	if snap.OpenDeviationCount() > 0 {
		return domain.ControlAck{}, domain.ControlIntent{}, domain.ConflictError{Code: "open_deviation_blocks_control", Message: "resolve deviations before ventilation"}
	}
	next := snap.Ventilation.NextStage()
	if req.Stage == "" {
		req.Stage = next
	}
	if req.Stage != next {
		return domain.ControlAck{}, domain.ControlIntent{}, domain.ConflictError{Code: "stage_order_violation", Message: fmt.Sprintf("expected %s got %s", next, req.Stage)}
	}
	if snap.Ventilation.ActiveStage != "" && snap.Ventilation.ActiveStage != req.Stage {
		return domain.ControlAck{}, domain.ControlIntent{}, domain.ConflictError{Code: "stage_mutex_conflict", Message: string(snap.Ventilation.ActiveStage)}
	}
	if snap.Ventilation.LastCommandRequest == req.Idempotency && snap.Ventilation.Ack != nil {
		return *snap.Ventilation.Ack, domain.ControlIntent{}, nil
	}
	commandID := snap.Operation.ID + "-" + string(req.Stage) + "-" + req.Idempotency
	intent := domain.ControlIntent{
		CommandID: commandID, OperationID: snap.Operation.ID, Stage: req.Stage, Idempotency: req.Idempotency,
		Expected: req.ExpectedStage, CreatedAt: now.UTC(), Status: domain.CommandPending,
	}
	if i.Store != nil {
		if err := i.Store.AppendOutbox(ctx, intent); err != nil {
			return domain.ControlAck{}, intent, err
		}
	}
	ack, err := i.Controller.Send(ctx, intent)
	if err != nil {
		return domain.ControlAck{}, intent, err
	}
	if i.Store != nil {
		if err := i.Store.MarkOutbox(ctx, commandID, ack); err != nil {
			return ack, intent, err
		}
	}
	if !ack.Accepted {
		return ack, intent, domain.BoundaryError{Boundary: domain.BoundaryControl, Code: "control_rejected", Message: ack.Message}
	}
	deadline := now.Add(30 * time.Minute).UTC()
	snap.Ventilation.Plan = domain.VentilationPlan
	snap.Ventilation.ActiveStage = req.Stage
	snap.Ventilation.StageDeadline = &deadline
	snap.Ventilation.CommandID = commandID
	snap.Ventilation.Ack = &ack
	snap.Ventilation.MutexToken = commandID
	snap.Ventilation.LastCommandRequest = req.Idempotency
	if !snap.Ventilation.StageComplete(req.Stage) {
		snap.Ventilation.CompletedStages = append(snap.Ventilation.CompletedStages, req.Stage)
	}
	if snap.Ventilation.NextStage() == domain.StageComplete {
		snap.Ventilation.ActiveStage = domain.StageComplete
	} else {
		snap.Ventilation.ActiveStage = ""
	}
	snap.Operation.Status = domain.StatusVentilating
	if snap.Ventilation.NextStage() == domain.StageComplete {
		snap.Operation.Status = domain.StatusVentilating
	}
	return ack, intent, nil
}

func (i Interlock) EmergencyStop(snapshot *domain.OperationSnapshot, reason string, now time.Time) error {
	if strings.TrimSpace(reason) == "" {
		return domain.ValidationError{Code: "emergency_reason_required", Message: "reason is required"}
	}
	if snapshot.Operation.Status == domain.StatusArchived || snapshot.Operation.Status == domain.StatusEntryUnlocked {
		return domain.ConflictError{Code: "emergency_stop_not_allowed", Message: string(snapshot.Operation.Status)}
	}
	snapshot.Operation.SafePreviousStatus = snapshot.Operation.Status
	snapshot.Operation.Status = domain.StatusEmergencyStop
	snapshot.Ventilation.AbortReason = reason
	snapshot.Ventilation.ActiveStage = ""
	snapshot.Ventilation.MutexToken = ""
	snapshot.Ventilation.CompletedStages = nil
	_ = now
	return nil
}

func (i Interlock) Reset(snapshot *domain.OperationSnapshot, reason, verifiedBy string, fresh bool, now time.Time) error {
	if snapshot.Operation.Status != domain.StatusEmergencyStop {
		return domain.ConflictError{Code: "reset_requires_emergency_stop", Message: string(snapshot.Operation.Status)}
	}
	if reason == "" || verifiedBy == "" {
		return domain.ValidationError{Code: "reset_record_incomplete", Message: "reason and verified_by are required"}
	}
	if !fresh || snapshot.OpenDeviationCount() > 0 {
		return domain.ConflictError{Code: "fresh_validation_required", Message: "fresh readings and closed deviations are required"}
	}
	restore := snapshot.Operation.SafePreviousStatus
	if restore == "" || restore == domain.StatusEmergencyStop {
		restore = domain.StatusExposing
	}
	snapshot.Operation.Status = restore
	snapshot.Operation.SafePreviousStatus = ""
	snapshot.Ventilation.AbortReason = ""
	snapshot.Ventilation.ResetRecords = append(snapshot.Ventilation.ResetRecords, domain.ResetRecord{Reason: reason, VerifiedAt: now.UTC(), VerifiedBy: verifiedBy, RestoredState: restore})
	return nil
}
