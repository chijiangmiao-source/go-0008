package app_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"grain-fumigation-interlock/internal/app"
	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/store"
)

func TestModel_BootstrapRecoversPendingVentilationCommands(t *testing.T) {
	at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		arrange func(t *testing.T, js *store.JSONStore)
		assert  func(t *testing.T, runtime *app.Runtime)
	}{
		{
			name: "replays a pending intent with its original command ID and applies the acknowledgement",
			arrange: func(t *testing.T, js *store.JSONStore) {
				t.Helper()
				snapshot := domain.OperationSnapshot{
					Operation:   domain.FumigationOperation{ID: "op-pending", Status: domain.StatusReadyToVent, Revision: 7},
					Ventilation: domain.VentilationExecution{Plan: domain.VentilationPlan},
				}
				if err := js.Create(context.Background(), snapshot, nil); err != nil {
					t.Fatal(err)
				}
				intent := domain.ControlIntent{
					CommandID: "controller-command-0042", OperationID: "op-pending", Stage: domain.StagePurge,
					Idempotency: "start-purge", CreatedAt: at, Status: domain.CommandPending,
				}
				if err := js.AppendOutbox(context.Background(), intent); err != nil {
					t.Fatal(err)
				}
			},
			assert: func(t *testing.T, runtime *app.Runtime) {
				t.Helper()
				if runtime.Report.PendingCommands != 1 {
					t.Fatalf("recovery report lost the persisted pending count: %#v", runtime.Report)
				}
				pending, err := runtime.Store.PendingOutbox(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if len(pending) != 0 {
					t.Fatalf("accepted command remained pending after bootstrap: %#v", pending)
				}
				got, err := runtime.Store.Get(context.Background(), "op-pending")
				if err != nil {
					t.Fatal(err)
				}
				if got.Operation.Status != domain.StatusVentilating {
					t.Fatalf("acknowledged recovery did not advance operation: got %s", got.Operation.Status)
				}
				if got.Ventilation.CommandID != "controller-command-0042" || got.Ventilation.MutexToken != "controller-command-0042" {
					t.Fatalf("recovery did not retain the original command ID: %#v", got.Ventilation)
				}
				if got.Ventilation.Ack == nil || !got.Ventilation.Ack.Accepted || got.Ventilation.Ack.CommandID != "controller-command-0042" {
					t.Fatalf("matching controller acknowledgement was not applied: %#v", got.Ventilation.Ack)
				}
				if !reflect.DeepEqual(got.Ventilation.CompletedStages, []domain.VentilationStage{domain.StagePurge}) {
					t.Fatalf("recovered stage was not completed exactly once: %#v", got.Ventilation.CompletedStages)
				}
			},
		},
		{
			name: "keeps report ordering while ignoring acknowledged commands and archived operations",
			arrange: func(t *testing.T, js *store.JSONStore) {
				t.Helper()
				for _, operation := range []domain.FumigationOperation{
					{ID: "op-z", Status: domain.StatusReadyToVent, Revision: 1},
					{ID: "op-a", Status: domain.StatusReadyToVent, Revision: 1},
					{ID: "op-archived", Status: domain.StatusArchived, Revision: 9},
				} {
					if err := js.Create(context.Background(), domain.OperationSnapshot{Operation: operation}, nil); err != nil {
						t.Fatal(err)
					}
				}
				intent := domain.ControlIntent{CommandID: "already-acked", OperationID: "op-a", Stage: domain.StagePurge, Idempotency: "done", CreatedAt: at}
				if err := js.AppendOutbox(context.Background(), intent); err != nil {
					t.Fatal(err)
				}
				ack := domain.ControlAck{CommandID: intent.CommandID, Stage: intent.Stage, Accepted: true, ReceivedAt: at, PhysicalRun: true}
				if err := js.MarkOutbox(context.Background(), intent.CommandID, ack); err != nil {
					t.Fatal(err)
				}
			},
			assert: func(t *testing.T, runtime *app.Runtime) {
				t.Helper()
				if runtime.Report.Operations != 3 || runtime.Report.PendingCommands != 0 {
					t.Fatalf("unexpected recovery counts: %#v", runtime.Report)
				}
				wantRecovered := []string{"op-a", "op-z"}
				if !reflect.DeepEqual(runtime.Report.RecoveredOperation, wantRecovered) {
					t.Fatalf("recovered operations = %#v, want %#v", runtime.Report.RecoveredOperation, wantRecovered)
				}
				active, err := runtime.Store.Get(context.Background(), "op-a")
				if err != nil {
					t.Fatal(err)
				}
				if active.Operation.Status != domain.StatusReadyToVent || active.Ventilation.Ack != nil || active.Ventilation.CommandID != "" {
					t.Fatalf("already acknowledged command was replayed: %#v", active)
				}
				archived, err := runtime.Store.Get(context.Background(), "op-archived")
				if err != nil {
					t.Fatal(err)
				}
				if archived.Operation.Status != domain.StatusArchived || archived.Operation.Revision != 9 {
					t.Fatalf("archived operation was recovered or mutated: %#v", archived.Operation)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := t.TempDir()
			js, err := store.OpenJSONStore(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			tt.arrange(t, js)

			runtime, err := app.Bootstrap(context.Background(), app.Config{
				DataDir:   dataDir,
				Anchor:    at,
				Standards: domain.Standards{Versions: domain.DefaultStandards(at)},
			})
			if err != nil {
				t.Fatal(err)
			}
			tt.assert(t, runtime)
		})
	}
}
