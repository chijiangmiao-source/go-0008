package ventilation

import (
	"context"
	"testing"
	"time"

	"grain-fumigation-interlock/internal/domain"
)

func ventSnapshot() domain.OperationSnapshot {
	return domain.OperationSnapshot{
		Operation:   domain.FumigationOperation{ID: "op", Status: domain.StatusReadyToVent, Revision: 4},
		Ventilation: domain.VentilationExecution{Plan: domain.VentilationPlan},
	}
}

func TestInterlockRejectsOutOfOrderStage(t *testing.T) {
	snap := ventSnapshot()
	_, _, err := Interlock{Controller: NewSimulatedController()}.StartOrAdvance(context.Background(), &snap, CommandRequest{Idempotency: "x", Stage: domain.StageDilution}, time.Now())
	if err == nil {
		t.Fatal("expected stage order violation")
	}
}

func TestSimulatedControllerReturnsSameAckForDuplicateCommand(t *testing.T) {
	controller := NewSimulatedController()
	intent := domain.ControlIntent{CommandID: "cmd", OperationID: "op", Stage: domain.StagePurge, Idempotency: "same", CreatedAt: time.Now()}
	first, err := controller.Send(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	second, err := controller.Send(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReceivedAt != second.ReceivedAt || !second.PhysicalRun {
		t.Fatalf("ack changed across duplicate: %#v %#v", first, second)
	}
}

func TestEmergencyStopRequiresFreshReset(t *testing.T) {
	snap := ventSnapshot()
	interlock := Interlock{Controller: NewSimulatedController()}
	if err := interlock.EmergencyStop(&snap, "fan fault", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := interlock.Reset(&snap, "fixed", "inspector", false, time.Now()); err == nil {
		t.Fatal("expected fresh validation conflict")
	}
	if err := interlock.Reset(&snap, "fixed", "inspector", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if snap.Operation.Status != domain.StatusReadyToVent {
		t.Fatalf("unexpected restore status %s", snap.Operation.Status)
	}
}
