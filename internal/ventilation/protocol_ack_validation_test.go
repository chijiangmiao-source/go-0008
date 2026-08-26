package ventilation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/ventilation"
)

type protocolAckController struct {
	ack domain.ControlAck
}

func (c protocolAckController) Send(context.Context, domain.ControlIntent) (domain.ControlAck, error) {
	return c.ack, nil
}

func TestModel_StaleOrMismatchedControlAckKeepsEntryLocked(t *testing.T) {
	now := time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)
	commandID := "op-clearance-current"
	tests := []struct {
		name string
		ack  domain.ControlAck
	}{
		{
			name: "late acknowledgement from previous command",
			ack: domain.ControlAck{
				CommandID: "op-clearance-previous", Stage: domain.StageClearance,
				Accepted: true, PhysicalRun: true, ReceivedAt: now,
			},
		},
		{
			name: "acknowledgement for different stage",
			ack: domain.ControlAck{
				CommandID: commandID, Stage: domain.StageDilution,
				Accepted: true, PhysicalRun: true, ReceivedAt: now,
			},
		},
		{
			name: "accepted response without confirmed physical execution",
			ack: domain.ControlAck{
				CommandID: commandID, Stage: domain.StageClearance,
				Accepted: true, PhysicalRun: false, ReceivedAt: now,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := protocolAckController{ack: tt.ack}
			snapshot := domain.OperationSnapshot{
				Operation: domain.FumigationOperation{ID: "op", Status: domain.StatusVentilating},
				Ventilation: domain.VentilationExecution{
					Plan:            domain.VentilationPlan,
					CompletedStages: []domain.VentilationStage{domain.StagePurge, domain.StageDilution},
				},
				Readings: []domain.SensorReading{{
					EventID: "safe-residual", Kind: domain.MeasureConcentration,
					StandardValue: 1, Disposition: domain.ReadingAccepted,
				}},
			}

			_, _, err := (ventilation.Interlock{Controller: controller}).StartOrAdvance(
				context.Background(), &snapshot,
				ventilation.CommandRequest{Idempotency: "current", Stage: domain.StageClearance}, now,
			)
			if err != nil {
				var boundary domain.BoundaryError
				if !errors.As(err, &boundary) || boundary.Boundary != domain.BoundaryControl || boundary.Code == "" {
					t.Fatalf("mismatched ack must yield a stable control-boundary error or safe recoverable state, got %v", err)
				}
			}

			if snapshot.Ventilation.NextStage() != domain.StageClearance || len(snapshot.Ventilation.CompletedStages) != 2 {
				t.Fatalf("mismatched ack completed the current stage: %#v", snapshot.Ventilation)
			}
			if snapshot.Ventilation.CommandID != "" || snapshot.Ventilation.Ack != nil || snapshot.Ventilation.MutexToken != "" {
				t.Fatalf("mismatched ack was committed as the current command: %#v", snapshot.Ventilation)
			}

			_, entryErr := (ventilation.EntryService{Controller: controller}).Request(
				context.Background(), &snapshot,
				ventilation.EntryRequest{RequestID: "person-entry", Idempotency: "entry"}, now,
			)
			var conflict domain.ConflictError
			if !errors.As(entryErr, &conflict) || conflict.Code != "ventilation_incomplete" {
				t.Fatalf("personnel entry must remain locked after mismatched ack, got %v", entryErr)
			}
			if snapshot.Operation.Status == domain.StatusEntryUnlocked || snapshot.Entry.CommittedAt != nil {
				t.Fatalf("personnel entry was unlocked: %#v", snapshot.Entry)
			}
		})
	}
}
