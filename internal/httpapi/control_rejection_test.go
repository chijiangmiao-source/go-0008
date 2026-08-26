package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"grain-fumigation-interlock/internal/app"
	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/httpapi"
	"grain-fumigation-interlock/internal/replay"
	"grain-fumigation-interlock/internal/store"
	"grain-fumigation-interlock/internal/ventilation"
)

type controlRejectionClock struct{ at time.Time }

func (c controlRejectionClock) Now() time.Time { return c.at }

func TestModel_ControlRejectionPersistsDeviationAndBlocksSafetyBoundaries(t *testing.T) {
	at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	port, err := store.OpenJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	controller := ventilation.NewSimulatedController()
	controller.FailFor[domain.StagePurge] = "purge fan interlock refused"
	service := app.NewService(
		port,
		domain.Standards{Versions: domain.DefaultStandards(at)},
		controlRejectionClock{at: at},
		controller,
	)
	created, err := service.CreateOperation(context.Background(), replay.SampleCreate(at))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Seal(context.Background(), created.ID, app.SealRequest{
		ExpectedRevision: created.Revision,
		SealedAt:         at,
	})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := service.SubmitReadings(context.Background(), created.ID, app.ReadingsRequest{
		Readings: replay.ExposureReadings(at, 95, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != domain.StatusReadyToVent {
		t.Fatalf("precondition: got status %s, want %s", ready.Status, domain.StatusReadyToVent)
	}

	handler := httpapi.New(service)
	post := func(t *testing.T, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload)))
		return recorder
	}
	get := func(t *testing.T) app.OperationStatusResponse {
		t.Helper()
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/operations/"+created.ID, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET operation status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
		}
		var response app.OperationStatusResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	commandPath := "/v1/operations/" + created.ID + "/ventilation/commands"
	entryPath := "/v1/operations/" + created.ID + "/entry-permit"

	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "controller rejection remains explicit",
			run: func(t *testing.T) {
				recorder := post(t, commandPath, app.VentilationCommandRequest{
					Idempotency:      "rejected-purge",
					Stage:            domain.StagePurge,
					ExpectedRevision: ready.Revision,
				})
				if recorder.Code != http.StatusUnprocessableEntity {
					t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusUnprocessableEntity, recorder.Body.String())
				}
				var response domain.BoundaryError
				if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response.Boundary != domain.BoundaryControl || response.Code != "control_rejected" {
					t.Fatalf("unexpected rejection: %#v", response)
				}
			},
		},
		{
			name: "failed command leaves one open control deviation",
			run: func(t *testing.T) {
				status := get(t)
				if len(status.OpenDeviation) != 1 {
					t.Fatalf("open deviations = %d, want exactly 1: %#v", len(status.OpenDeviation), status.OpenDeviation)
				}
				deviation := status.OpenDeviation[0]
				if deviation.Type != domain.DeviationControl || deviation.Status != domain.DeviationOpen {
					t.Fatalf("unexpected persisted deviation: %#v", deviation)
				}
			},
		},
		{
			name: "retry does not duplicate deviation",
			run: func(t *testing.T) {
				status := get(t)
				recorder := post(t, commandPath, app.VentilationCommandRequest{
					Idempotency:      "rejected-purge",
					Stage:            domain.StagePurge,
					ExpectedRevision: status.Operation.Revision,
				})
				if recorder.Code != http.StatusConflict && recorder.Code != http.StatusUnprocessableEntity {
					t.Fatalf("retry status = %d, want 409 or 422; body=%s", recorder.Code, recorder.Body.String())
				}
				status = get(t)
				if len(status.OpenDeviation) != 1 {
					t.Fatalf("open deviations after retry = %d, want exactly 1: %#v", len(status.OpenDeviation), status.OpenDeviation)
				}
			},
		},
		{
			name: "later ventilation remains blocked",
			run: func(t *testing.T) {
				status := get(t)
				recorder := post(t, commandPath, app.VentilationCommandRequest{
					Idempotency:      "different-purge-attempt",
					Stage:            domain.StagePurge,
					ExpectedRevision: status.Operation.Revision,
				})
				if recorder.Code != http.StatusConflict {
					t.Fatalf("later ventilation status = %d, want %d; body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
				}
			},
		},
		{
			name: "entry remains blocked",
			run: func(t *testing.T) {
				recorder := post(t, entryPath, app.EntryRequest{
					RequestID:   "entry-after-control-failure",
					Idempotency: "entry-after-control-failure",
					Operator:    "safety-officer",
				})
				if recorder.Code != http.StatusConflict {
					t.Fatalf("entry status = %d, want %d; body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}
