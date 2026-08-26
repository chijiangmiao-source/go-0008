package app_test

import (
	"context"
	"testing"
	"time"

	"grain-fumigation-interlock/internal/app"
	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/replay"
	"grain-fumigation-interlock/internal/store"
	"grain-fumigation-interlock/internal/ventilation"
)

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

func newTestService(t *testing.T, at time.Time) (*app.Service, *store.JSONStore) {
	t.Helper()
	js, err := store.OpenJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	standards := domain.Standards{Versions: domain.DefaultStandards(at)}
	return app.NewService(js, standards, fixedClock{at: at}, ventilation.NewSimulatedController()), js
}

func createAndSeal(t *testing.T, svc *app.Service, at time.Time) string {
	t.Helper()
	ctx := context.Background()
	created, err := svc.CreateOperation(ctx, replay.SampleCreate(at))
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != domain.StatusRegistered || created.StandardVersion.Version == "" {
		t.Fatalf("unexpected create response: %#v", created)
	}
	sealed, err := svc.Seal(ctx, created.ID, app.SealRequest{ExpectedRevision: created.Revision, SealedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Operation.Status != domain.StatusExposing {
		t.Fatalf("expected exposing after seal, got %s", sealed.Operation.Status)
	}
	return created.ID
}

func TestFullWorkflowArchivesImmutableManifest(t *testing.T) {
	at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	svc, _ := newTestService(t, at)
	id := createAndSeal(t, svc, at)
	ctx := context.Background()
	readResp, err := svc.SubmitReadings(ctx, id, app.ReadingsRequest{Readings: replay.ExposureReadings(at, 95, false)})
	if err != nil {
		t.Fatal(err)
	}
	if readResp.Status != domain.StatusReadyToVent {
		t.Fatalf("expected ready_to_vent, got %s", readResp.Status)
	}
	for _, stage := range domain.VentilationPlan {
		status, _ := svc.Get(ctx, id)
		if _, err := svc.VentilationCommand(ctx, id, app.VentilationCommandRequest{Idempotency: string(stage), Stage: stage, ExpectedRevision: status.Operation.Revision}); err != nil {
			t.Fatalf("stage %s: %v", stage, err)
		}
	}
	if _, err := svc.EntryPermit(ctx, id, app.EntryRequest{RequestID: "entry", Idempotency: "entry", Operator: "qa"}); err != nil {
		t.Fatal(err)
	}
	archived, err := svc.Archive(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != domain.StatusArchived || !archived.Manifest.Verify() {
		t.Fatalf("archive not sealed: %#v", archived.Manifest)
	}
}

func TestSealRejectsRevisionConflict(t *testing.T) {
	at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	svc, _ := newTestService(t, at)
	created, err := svc.CreateOperation(context.Background(), replay.SampleCreate(at))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Seal(context.Background(), created.ID, app.SealRequest{ExpectedRevision: created.Revision + 1, SealedAt: at}); err == nil {
		t.Fatal("expected revision conflict")
	}
}

func TestEvidencePaginationIncludesEventsReadingsAndSegments(t *testing.T) {
	at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	svc, _ := newTestService(t, at)
	id := createAndSeal(t, svc, at)
	if _, err := svc.SubmitReadings(context.Background(), id, app.ReadingsRequest{Readings: replay.ExposureReadings(at, 3, false)}); err != nil {
		t.Fatal(err)
	}
	page, err := svc.Evidence(context.Background(), id, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total < 8 || len(page.Items) != 5 || page.NextOffset == 0 {
		t.Fatalf("unexpected evidence page: %#v", page)
	}
}
