package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"grain-fumigation-interlock/internal/app"
	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/httpapi"
	"grain-fumigation-interlock/internal/replay"
	"grain-fumigation-interlock/internal/store"
)

var errControllerCrash = errors.New("controller process stopped after accepting dispatch")

type crashAfterOutboxController struct {
	failOnSend int
	sends      int
	failedID   string
}

func (c *crashAfterOutboxController) Send(ctx context.Context, intent domain.ControlIntent) (domain.ControlAck, error) {
	c.sends++
	if c.sends == c.failOnSend {
		c.failedID = intent.CommandID
		return domain.ControlAck{}, errControllerCrash
	}
	return domain.ControlAck{
		CommandID: intent.CommandID, Stage: intent.Stage, Accepted: true,
		Controller: "acceptance-controller", ReceivedAt: time.Date(2026, 8, 26, 9, 0, c.sends, 0, time.UTC),
		PhysicalRun: true,
	}, nil
}

func modelHTTPReadySnapshot(stage domain.VentilationStage) domain.OperationSnapshot {
	completed := []domain.VentilationStage{}
	status := domain.StatusReadyToVent
	switch stage {
	case domain.StageDilution:
		completed = []domain.VentilationStage{domain.StagePurge}
		status = domain.StatusVentilating
	case domain.StageClearance:
		completed = []domain.VentilationStage{domain.StagePurge, domain.StageDilution}
		status = domain.StatusVentilating
	case domain.StageComplete:
		completed = append([]domain.VentilationStage(nil), domain.VentilationPlan...)
		status = domain.StatusVentilating
	}
	return domain.OperationSnapshot{
		Operation:   domain.FumigationOperation{ID: "op-http-" + string(stage), Status: status, Revision: 7},
		Ventilation: domain.VentilationExecution{Plan: domain.VentilationPlan, CompletedStages: completed},
		Readings:    []domain.SensorReading{{EventID: "residual-ok", Kind: domain.MeasureConcentration, StandardValue: 1, Disposition: domain.ReadingAccepted}},
	}
}

func TestModel_ControlCommandsAreRecoverableAcrossRestart(t *testing.T) {
	anchor := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		workflow   string
		stage      domain.VentilationStage
		failOnSend int
	}{
		{name: "replay purge", workflow: "replay", stage: domain.StagePurge, failOnSend: 1},
		{name: "replay dilution", workflow: "replay", stage: domain.StageDilution, failOnSend: 2},
		{name: "replay clearance", workflow: "replay", stage: domain.StageClearance, failOnSend: 3},
		{name: "replay entry unlock", workflow: "replay", stage: domain.StageComplete, failOnSend: 4},
		{name: "HTTP purge", workflow: "http", stage: domain.StagePurge, failOnSend: 1},
		{name: "HTTP dilution", workflow: "http", stage: domain.StageDilution, failOnSend: 1},
		{name: "HTTP clearance", workflow: "http", stage: domain.StageClearance, failOnSend: 1},
		{name: "HTTP entry unlock", workflow: "http", stage: domain.StageComplete, failOnSend: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			js, err := store.OpenJSONStore(root)
			if err != nil {
				t.Fatal(err)
			}
			controller := &crashAfterOutboxController{failOnSend: tc.failOnSend}
			svc := app.NewService(js, domain.Standards{Versions: domain.DefaultStandards(anchor)}, &replay.FixedClock{T: anchor}, controller)

			if tc.workflow == "replay" {
				if _, err = replay.RunDeterministic(ctx, svc, anchor); !errors.Is(err, errControllerCrash) {
					t.Fatalf("replay should stop at simulated process exit, got %v", err)
				}
			} else {
				snap := modelHTTPReadySnapshot(tc.stage)
				if err = js.Create(ctx, snap, nil); err != nil {
					t.Fatal(err)
				}
				path := "/v1/operations/" + snap.Operation.ID + "/ventilation/commands"
				payload := interface{}(app.VentilationCommandRequest{Idempotency: "request-1", Stage: tc.stage, ExpectedRevision: snap.Operation.Revision})
				if tc.stage == domain.StageComplete {
					path = "/v1/operations/" + snap.Operation.ID + "/entry-permit"
					payload = app.EntryRequest{RequestID: "entry-1", Idempotency: "request-1", Operator: "acceptance"}
				}
				body, marshalErr := json.Marshal(payload)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
				rec := httptest.NewRecorder()
				httpapi.New(svc).ServeHTTP(rec, req)
				if rec.Code != http.StatusInternalServerError {
					t.Fatalf("HTTP workflow should expose the simulated interruption, got %d: %s", rec.Code, rec.Body.String())
				}
			}

			if controller.failedID == "" {
				t.Fatal("controller did not receive the target command")
			}

			reopened, err := store.OpenJSONStore(root)
			if err != nil {
				t.Fatal(err)
			}
			pending, err := reopened.PendingOutbox(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 1 {
				t.Fatalf("restart lost the unconfirmed command: got %d pending commands", len(pending))
			}
			if pending[0].CommandID != controller.failedID || pending[0].Status != domain.CommandPending {
				t.Fatalf("restart must retain the original command ID %q as pending, got %#v", controller.failedID, pending[0])
			}
			if pending[0].Stage != tc.stage || !strings.Contains(pending[0].CommandID, pending[0].Idempotency) {
				t.Fatalf("recovered command does not match the requested control action: %#v", pending[0])
			}
			report, err := store.Recover(ctx, reopened)
			if err != nil {
				t.Fatal(err)
			}
			if report.PendingCommands != 1 {
				t.Fatalf("recover should report one command available for redelivery, got %#v", report)
			}
		})
	}
}
