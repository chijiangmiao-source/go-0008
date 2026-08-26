package store

import (
	"context"
	"testing"
	"time"

	"grain-fumigation-interlock/internal/domain"
)

func TestJSONStoreRecoversSnapshotsAndEvents(t *testing.T) {
	dir := t.TempDir()
	js, err := OpenJSONStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	snap := domain.OperationSnapshot{Operation: domain.FumigationOperation{ID: "op", Status: domain.StatusRegistered, Revision: 1}}
	if err := js.Create(context.Background(), snap, []domain.EventRecord{{Type: "created", OccurredAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenJSONStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(context.Background(), "op")
	if err != nil {
		t.Fatal(err)
	}
	if got.Operation.Status != domain.StatusRegistered || len(got.Events) != 1 {
		t.Fatalf("unexpected recovery: %#v", got)
	}
}

func TestJSONStoreRevisionConflictPreventsMutation(t *testing.T) {
	js, err := OpenJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snap := domain.OperationSnapshot{Operation: domain.FumigationOperation{ID: "op", Status: domain.StatusRegistered, Revision: 1}}
	if err := js.Create(context.Background(), snap, nil); err != nil {
		t.Fatal(err)
	}
	_, err = js.Update(context.Background(), "op", 2, func(snapshot *domain.OperationSnapshot) ([]domain.EventRecord, error) {
		snapshot.Operation.Status = domain.StatusSealed
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected revision conflict")
	}
	got, _ := js.Get(context.Background(), "op")
	if got.Operation.Status != domain.StatusRegistered {
		t.Fatalf("mutation leaked despite conflict: %s", got.Operation.Status)
	}
}

func TestRecoveryReportListsPendingCommands(t *testing.T) {
	js, err := OpenJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snap := domain.OperationSnapshot{Operation: domain.FumigationOperation{ID: "op", Status: domain.StatusReadyToVent, Revision: 1}}
	if err := js.Create(context.Background(), snap, nil); err != nil {
		t.Fatal(err)
	}
	if err := js.AppendOutbox(context.Background(), domain.ControlIntent{CommandID: "cmd", OperationID: "op", Stage: domain.StagePurge, Idempotency: "a", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	report, err := Recover(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	if report.Operations != 1 || report.PendingCommands != 1 || report.RecoveredOperation[0] != "op" {
		t.Fatalf("unexpected report: %#v", report)
	}
}
