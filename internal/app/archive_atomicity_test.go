package app_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"grain-fumigation-interlock/internal/app"
	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/store"
	"grain-fumigation-interlock/internal/ventilation"
)

type archiveClock struct{ at time.Time }

func (c *archiveClock) Now() time.Time { return c.at }

type failingArchiveStore struct {
	store.EventStorePort
	failures  int
	attempted []domain.ArchiveManifest
	saved     []domain.ArchiveManifest
}

func (s *failingArchiveStore) SaveArchive(ctx context.Context, manifest domain.ArchiveManifest) error {
	s.attempted = append(s.attempted, manifest)
	if s.failures > 0 {
		s.failures--
		return errors.New("injected archive persistence failure")
	}
	if err := s.EventStorePort.SaveArchive(ctx, manifest); err != nil {
		return err
	}
	s.saved = append(s.saved, manifest)
	return nil
}

func archiveReadyService(t *testing.T, failures int) (*app.Service, *archiveClock, *failingArchiveStore, string) {
	t.Helper()
	at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	standards := domain.Standards{Versions: domain.DefaultStandards(at)}
	standard := standards.Versions[0]
	js, err := store.OpenJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := "op-archive-atomicity"
	committed := at.Add(-time.Minute)
	snapshot := domain.OperationSnapshot{
		Operation: domain.FumigationOperation{
			ID: id, Organization: "inspection-lab", Site: "bin-7", Carrier: domain.CarrierSilo,
			Agent: "phosphine", Status: domain.StatusEntryUnlocked, Revision: 7,
			StandardVersion: standard.Version, CreatedAt: at.Add(-2 * time.Hour), UpdatedAt: committed,
		},
		Ledger: domain.ExposureLedger{Segments: []domain.ExposureSegment{{
			Start: at.Add(-2 * time.Hour), End: at.Add(-time.Hour), Included: true,
			StandardVersion: standard.Version,
		}}},
		Entry: domain.EntryPermit{
			RequestID: "entry-1", UnlockCommandID: "unlock-1", CommittedAt: &committed,
			Ack: &domain.ControlAck{CommandID: "unlock-1", Accepted: true, ReceivedAt: committed, PhysicalRun: true},
		},
	}
	if err := js.Create(context.Background(), snapshot, []domain.EventRecord{{Type: "entry.unlocked", OccurredAt: committed}}); err != nil {
		t.Fatal(err)
	}
	port := &failingArchiveStore{EventStorePort: js, failures: failures}
	clock := &archiveClock{at: at}
	return app.NewService(port, standards, clock, ventilation.NewSimulatedController()), clock, port, id
}

func archiveCreatedEvents(events []domain.EventRecord) int {
	count := 0
	for _, event := range events {
		if event.Type == "archive.created" {
			count++
		}
	}
	return count
}

func TestModel_ArchivePersistenceIsAtomicAndRetryable(t *testing.T) {
	cases := []struct {
		name       string
		failures   int
		retryFirst bool
	}{
		{name: "persistence failure leaves operation retryable and commits once", failures: 1, retryFirst: true},
		{name: "completed archive is immutable and rejects a different archive", failures: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc, clock, port, id := archiveReadyService(t, tc.failures)

			first, err := svc.Archive(ctx, id)
			if tc.retryFirst {
				if err == nil {
					t.Fatal("expected the injected manifest persistence failure")
				}
				visible, getErr := svc.Get(ctx, id)
				if getErr != nil {
					t.Fatal(getErr)
				}
				if visible.Operation.Status == domain.StatusArchived {
					t.Fatalf("failed archive became externally terminal: status=%s", visible.Operation.Status)
				}
				if len(port.attempted) != 1 {
					t.Fatalf("expected one attempted manifest, got %d", len(port.attempted))
				}
				first, err = svc.Archive(ctx, id)
			}
			if err != nil {
				t.Fatalf("archive request did not complete: %v", err)
			}
			if first.Status != domain.StatusArchived || !first.Manifest.Verify() {
				t.Fatalf("invalid completed archive: %#v", first)
			}
			if !first.Manifest.IncludesEntryAck || !first.Manifest.IncludesLedger {
				t.Fatalf("manifest omitted required entry or ledger evidence: %#v", first.Manifest)
			}
			if tc.retryFirst && !reflect.DeepEqual(port.attempted[0], first.Manifest) {
				t.Fatalf("retry produced a different manifest:\nfirst: %#v\nretry: %#v", port.attempted[0], first.Manifest)
			}

			clock.at = clock.at.Add(time.Hour)
			if _, err := svc.Archive(ctx, id); err == nil {
				t.Fatal("expected an already archived operation to reject a different archive")
			}
			final, err := port.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if final.Operation.Status != domain.StatusArchived || final.Archive == nil {
				t.Fatalf("completed archive state was lost: %#v", final.Operation)
			}
			if final.Archive.IntegrityDigest != first.Manifest.IntegrityDigest || !final.Archive.Verify() {
				t.Fatal("persisted manifest digest changed after the rejected request")
			}
			if len(port.saved) != 1 || archiveCreatedEvents(final.Events) != 1 {
				t.Fatalf("expected one terminal archive and event, got saves=%d events=%d", len(port.saved), archiveCreatedEvents(final.Events))
			}
		})
	}
}
