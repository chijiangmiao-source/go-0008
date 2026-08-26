package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"grain-fumigation-interlock/internal/app"
	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/replay"
	"grain-fumigation-interlock/internal/store"
	"grain-fumigation-interlock/internal/ventilation"
)

func TestModel_StateAndEvidenceCommitAtomically(t *testing.T) {
	tests := []struct {
		name   string
		reopen bool
	}{
		{name: "failed update is invisible to the running service"},
		{name: "failed update is invisible after restart", reopen: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
			standards := domain.Standards{Versions: domain.DefaultStandards(at)}
			clock := fixedClock{at: at}

			js, err := store.OpenJSONStore(root)
			if err != nil {
				t.Fatal(err)
			}
			svc := app.NewService(js, standards, clock, ventilation.NewSimulatedController())
			created, err := svc.CreateOperation(ctx, replay.SampleCreate(at))
			if err != nil {
				t.Fatal(err)
			}

			if _, err := svc.Seal(ctx, created.ID, app.SealRequest{ExpectedRevision: created.Revision + 1, SealedAt: at}); err == nil {
				t.Fatal("expected revision conflict before fault injection")
			}

			eventTempPath := filepath.Join(root, "events", created.ID+".json.tmp")
			if err := os.Mkdir(eventTempPath, 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Seal(ctx, created.ID, app.SealRequest{ExpectedRevision: created.Revision, SealedAt: at}); err == nil {
				t.Fatal("expected the injected event persistence failure")
			}

			observed := svc
			if tt.reopen {
				reopened, err := store.OpenJSONStore(root)
				if err != nil {
					t.Fatal(err)
				}
				observed = app.NewService(reopened, standards, clock, ventilation.NewSimulatedController())
			}

			status, err := observed.Get(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			if status.Operation.Status != domain.StatusRegistered || status.Operation.Revision != created.Revision {
				t.Fatalf("failed update became visible: status=%s revision=%d", status.Operation.Status, status.Operation.Revision)
			}
			evidence, err := observed.Evidence(ctx, created.ID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			if evidence.Total != 1 {
				t.Fatalf("failed update changed evidence: total=%d", evidence.Total)
			}
			registered, ok := evidence.Items[0].(domain.EventRecord)
			if !ok || registered.Type != "operation.registered" {
				t.Fatalf("unexpected original evidence: %#v", evidence.Items[0])
			}

			if err := os.Remove(eventTempPath); err != nil {
				t.Fatal(err)
			}
			sealed, err := observed.Seal(ctx, created.ID, app.SealRequest{ExpectedRevision: created.Revision, SealedAt: at})
			if err != nil {
				t.Fatalf("retry after storage recovery: %v", err)
			}
			if sealed.Operation.Status != domain.StatusExposing || sealed.Operation.Revision != created.Revision+1 {
				t.Fatalf("unexpected successful retry: status=%s revision=%d", sealed.Operation.Status, sealed.Operation.Revision)
			}
			evidence, err = observed.Evidence(ctx, created.ID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			if evidence.Total != 2 {
				t.Fatalf("successful retry evidence total=%d, want 2", evidence.Total)
			}
			sealedEvent, ok := evidence.Items[1].(domain.EventRecord)
			if !ok || sealedEvent.Type != "operation.sealed" || sealedEvent.Version != sealed.Operation.Revision || sealedEvent.Sequence != registered.Sequence+1 {
				t.Fatalf("successful retry has non-contiguous or mismatched evidence: %#v", evidence.Items[1])
			}
		})
	}
}
