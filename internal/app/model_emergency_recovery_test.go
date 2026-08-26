package app_test

import (
	"context"
	"testing"

	"grain-fumigation-interlock/internal/app"
	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/store"
	"grain-fumigation-interlock/internal/ventilation"
)

func TestModel_EmergencyRecoveryPreservesVentilationProgress(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, ctx context.Context, svc *app.Service, id string)
	}{
		{
			name: "emergency stop freezes control and reset resumes at dilution",
			run: func(t *testing.T, ctx context.Context, svc *app.Service, id string) {
				stopped, err := svc.EmergencyStop(ctx, id, app.EmergencyStopRequest{Reason: "fan vibration"})
				if err != nil {
					t.Fatalf("emergency stop: %v", err)
				}
				if stopped.Operation.Status != domain.StatusEmergencyStop {
					t.Fatalf("status after emergency stop = %s, want %s", stopped.Operation.Status, domain.StatusEmergencyStop)
				}
				if stopped.Ventilation.ActiveStage != domain.StageNone || stopped.Ventilation.MutexToken != "" {
					t.Fatalf("active control was not frozen: stage=%s mutex=%q", stopped.Ventilation.ActiveStage, stopped.Ventilation.MutexToken)
				}

				queried, err := svc.Get(ctx, id)
				if err != nil {
					t.Fatalf("query stopped operation: %v", err)
				}
				if len(queried.Ventilation.CompletedStages) != 1 || queried.Ventilation.CompletedStages[0] != domain.StagePurge {
					t.Fatalf("completed stages after emergency stop = %v, want [purge]", queried.Ventilation.CompletedStages)
				}

				reset, err := svc.Reset(ctx, id, app.ResetRequest{Reason: "fan inspected", VerifiedBy: "site-reviewer", FreshReadingsVerified: true})
				if err != nil {
					t.Fatalf("safe reset: %v", err)
				}
				if reset.Operation.Status != domain.StatusVentilating {
					t.Fatalf("status after reset = %s, want %s", reset.Operation.Status, domain.StatusVentilating)
				}
				if len(reset.Ventilation.CompletedStages) != 1 || reset.Ventilation.CompletedStages[0] != domain.StagePurge {
					t.Fatalf("completed stages after reset = %v, want [purge]", reset.Ventilation.CompletedStages)
				}

				advanced, err := svc.VentilationCommand(ctx, id, app.VentilationCommandRequest{
					Idempotency:      "resume-dilution",
					Stage:            domain.StageDilution,
					ExpectedRevision: reset.Operation.Revision,
				})
				if err != nil {
					t.Fatalf("resume at first unfinished stage: %v", err)
				}
				if len(advanced.Ventilation.CompletedStages) != 2 ||
					advanced.Ventilation.CompletedStages[0] != domain.StagePurge ||
					advanced.Ventilation.CompletedStages[1] != domain.StageDilution {
					t.Fatalf("completed stages after resume = %v, want [purge dilution]", advanced.Ventilation.CompletedStages)
				}
				if advanced.Ventilation.NextStage() != domain.StageClearance {
					t.Fatalf("next stage after resume = %s, want %s", advanced.Ventilation.NextStage(), domain.StageClearance)
				}
			},
		},
		{
			name: "emergency stop without a reason is rejected",
			run: func(t *testing.T, ctx context.Context, svc *app.Service, id string) {
				if _, err := svc.EmergencyStop(ctx, id, app.EmergencyStopRequest{}); err == nil {
					t.Fatal("emergency stop without a reason succeeded")
				}
				queried, err := svc.Get(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if queried.Operation.Status != domain.StatusVentilating || len(queried.Ventilation.CompletedStages) != 1 {
					t.Fatalf("rejected stop changed operation: status=%s completed=%v", queried.Operation.Status, queried.Ventilation.CompletedStages)
				}
			},
		},
		{
			name: "reset without fresh verification is rejected",
			run: func(t *testing.T, ctx context.Context, svc *app.Service, id string) {
				if _, err := svc.EmergencyStop(ctx, id, app.EmergencyStopRequest{Reason: "fan vibration"}); err != nil {
					t.Fatal(err)
				}
				if _, err := svc.Reset(ctx, id, app.ResetRequest{Reason: "inspection pending", VerifiedBy: "site-reviewer"}); err == nil {
					t.Fatal("reset without fresh verification succeeded")
				}
				queried, err := svc.Get(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if queried.Operation.Status != domain.StatusEmergencyStop {
					t.Fatalf("status after rejected reset = %s, want %s", queried.Operation.Status, domain.StatusEmergencyStop)
				}
			},
		},
		{
			name: "normal stage order remains enforced",
			run: func(t *testing.T, ctx context.Context, svc *app.Service, id string) {
				status, err := svc.Get(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := svc.VentilationCommand(ctx, id, app.VentilationCommandRequest{Idempotency: "skip-dilution", Stage: domain.StageClearance, ExpectedRevision: status.Operation.Revision}); err == nil {
					t.Fatal("out-of-order clearance command succeeded")
				}
			},
		},
		{
			name: "active stage mutex remains enforced",
			run: func(t *testing.T, ctx context.Context, svc *app.Service, id string) {
				status, err := svc.Get(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := svc.VentilationCommand(ctx, id, app.VentilationCommandRequest{Idempotency: "competing-dilution", Stage: domain.StageDilution, ExpectedRevision: status.Operation.Revision}); err == nil {
					t.Fatal("command succeeded while another stage held the mutex")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			js, err := store.OpenJSONStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			activeStage := domain.StageDilution
			mutexToken := "in-flight-dilution"
			if tc.name == "active stage mutex remains enforced" {
				activeStage = domain.StageClearance
				mutexToken = "in-flight-clearance"
			}
			const id = "operation-under-ventilation"
			snapshot := domain.OperationSnapshot{
				Operation: domain.FumigationOperation{ID: id, Status: domain.StatusVentilating, Revision: 7},
				Ventilation: domain.VentilationExecution{
					Plan:            domain.VentilationPlan,
					ActiveStage:     activeStage,
					CompletedStages: []domain.VentilationStage{domain.StagePurge},
					MutexToken:      mutexToken,
				},
			}
			if err := js.Create(ctx, snapshot, nil); err != nil {
				t.Fatal(err)
			}
			svc := app.NewService(js, domain.Standards{}, nil, ventilation.NewSimulatedController())
			tc.run(t, ctx, svc, id)
		})
	}
}
