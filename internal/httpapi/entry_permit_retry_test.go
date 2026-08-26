package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"grain-fumigation-interlock/internal/app"
	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/httpapi"
	"grain-fumigation-interlock/internal/store"
)

type entryPermitRecordingController struct {
	entryCalls int
}

func (c *entryPermitRecordingController) Send(_ context.Context, intent domain.ControlIntent) (domain.ControlAck, error) {
	if intent.Stage == domain.StageComplete {
		c.entryCalls++
	}
	return domain.ControlAck{
		CommandID:   intent.CommandID,
		Stage:       intent.Stage,
		Accepted:    true,
		Controller:  "entry-test-controller",
		ReceivedAt:  intent.CreatedAt.Add(time.Second),
		Message:     "accepted",
		PhysicalRun: true,
	}, nil
}

func TestModel_EntryPermitRetryReturnsCommittedPermit(t *testing.T) {
	cases := []struct {
		name       string
		deliveries int
	}{
		{name: "retry_after_lost_response", deliveries: 1},
		{name: "repeated_duplicate_delivery", deliveries: 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
			operationID := "op-entry-retry-" + tc.name
			port, err := store.OpenJSONStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			snapshot := domain.OperationSnapshot{
				Operation: domain.FumigationOperation{
					ID: operationID, Status: domain.StatusVentilating, Revision: 7,
					CreatedAt: at, UpdatedAt: at,
				},
				Ventilation: domain.VentilationExecution{
					Plan:            append([]domain.VentilationStage(nil), domain.VentilationPlan...),
					CompletedStages: append([]domain.VentilationStage(nil), domain.VentilationPlan...),
				},
				Readings: []domain.SensorReading{{
					EventID: "fresh-residual", Kind: domain.MeasureConcentration,
					StandardValue: 1.2, Disposition: domain.ReadingAccepted,
					CorrectedTime: at, ReceivedAt: at,
				}},
			}
			if err := port.Create(context.Background(), snapshot, nil); err != nil {
				t.Fatal(err)
			}

			controller := &entryPermitRecordingController{}
			service := app.NewService(port, domain.Standards{Versions: domain.DefaultStandards(at)}, nil, controller)
			server := httpapi.New(service)
			path := "/v1/operations/" + operationID + "/entry-permit"
			body := `{"request_id":"entry-request-010","operator":"safety-officer","idempotency":"unlock-010"}`

			first := httptest.NewRecorder()
			server.ServeHTTP(first, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
			if first.Code != http.StatusOK {
				t.Fatalf("initial admission was not committed: status=%d body=%s", first.Code, first.Body.String())
			}

			get := httptest.NewRecorder()
			server.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/v1/operations/"+operationID, nil))
			if get.Code != http.StatusOK {
				t.Fatalf("GET after lost response returned %d: %s", get.Code, get.Body.String())
			}
			var persisted app.OperationStatusResponse
			if err := json.NewDecoder(get.Body).Decode(&persisted); err != nil {
				t.Fatal(err)
			}
			if persisted.Operation.Status != domain.StatusEntryUnlocked || persisted.Entry.CommittedAt == nil {
				t.Fatalf("entry permit was not persisted: %#v", persisted.Entry)
			}

			for delivery := 0; delivery < tc.deliveries; delivery++ {
				retry := httptest.NewRecorder()
				server.ServeHTTP(retry, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
				if retry.Code != http.StatusOK {
					t.Fatalf("duplicate delivery %d returned status=%d body=%s", delivery+1, retry.Code, retry.Body.String())
				}
				var returned domain.OperationSnapshot
				if err := json.NewDecoder(retry.Body).Decode(&returned); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(returned.Entry, persisted.Entry) {
					t.Fatalf("duplicate delivery returned a different permit:\nwant %#v\n got %#v", persisted.Entry, returned.Entry)
				}
			}
			if controller.entryCalls != 1 {
				t.Fatalf("unlock controller called %d times; want exactly once", controller.entryCalls)
			}
		})
	}
}
