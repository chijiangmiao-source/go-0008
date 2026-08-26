package app_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"grain-fumigation-interlock/internal/app"
	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/replay"
	"grain-fumigation-interlock/internal/store"
	"grain-fumigation-interlock/internal/ventilation"
)

type cancellationTestClock struct{ at time.Time }

func (c cancellationTestClock) Now() time.Time { return c.at }

type cancellationObservingStore struct {
	*store.JSONStore
	target  string
	entered chan struct{}
	proceed <-chan struct{}
	cancel  context.CancelFunc
	once    sync.Once
}

func (s *cancellationObservingStore) Update(ctx context.Context, id string, revision int64, mutate func(*domain.OperationSnapshot) ([]domain.EventRecord, error)) (domain.OperationSnapshot, error) {
	if id != s.target {
		return s.JSONStore.Update(ctx, id, revision, mutate)
	}
	return s.JSONStore.Update(ctx, id, revision, func(snapshot *domain.OperationSnapshot) ([]domain.EventRecord, error) {
		s.once.Do(func() { close(s.entered) })
		if s.cancel != nil {
			s.cancel()
		}
		if s.proceed != nil {
			select {
			case <-s.proceed:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return mutate(snapshot)
	})
}

func cancellationTestService(t *testing.T, at time.Time) (*app.Service, *cancellationObservingStore) {
	t.Helper()
	backing, err := store.OpenJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	observed := &cancellationObservingStore{JSONStore: backing, entered: make(chan struct{})}
	standards := domain.Standards{Versions: domain.DefaultStandards(at)}
	return app.NewService(observed, standards, cancellationTestClock{at: at}, ventilation.NewSimulatedController()), observed
}

func cancellationCreateAndSeal(t *testing.T, service *app.Service, at time.Time, site string) string {
	t.Helper()
	req := replay.SampleCreate(at)
	req.Site = site
	created, err := service.CreateOperation(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Seal(context.Background(), created.ID, app.SealRequest{ExpectedRevision: created.Revision, SealedAt: at}); err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func TestModel_CanceledReadingsAreAtomicAndOperationScoped(t *testing.T) {
	at := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "cancellation before commit leaves no business effects",
			run: func(t *testing.T) {
				service, observed := cancellationTestService(t, at)
				id := cancellationCreateAndSeal(t, service, at, "cancel-bin")
				before, err := observed.JSONStore.Get(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}

				ctx, cancel := context.WithCancel(context.Background())
				observed.target = id
				observed.cancel = cancel
				readings := make([]domain.RawReading, 20000)
				for i := range readings {
					readings[i] = domain.RawReading{
						EventID: fmt.Sprintf("cancel-%05d", i), ProbeID: "p-n-1", Sequence: int64(i + 1),
						DeviceTime: at.Add(time.Duration(i) * time.Second), ReceivedAt: at.Add(time.Duration(i+1) * time.Second),
						Value: 320, Unit: "ppm",
					}
				}
				done := make(chan error, 1)
				go func() {
					_, submitErr := service.SubmitReadings(ctx, id, app.ReadingsRequest{Readings: readings})
					done <- submitErr
				}()

				select {
				case err = <-done:
					if !errors.Is(err, context.Canceled) {
						t.Errorf("SubmitReadings error = %v, want context.Canceled", err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("canceled readings submission did not stop promptly")
				}
				after, err := observed.JSONStore.Get(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("canceled batch changed persisted snapshot: revision %d -> %d, readings %d -> %d, events %d -> %d, ledger segments %d -> %d",
						before.Operation.Revision, after.Operation.Revision, len(before.Readings), len(after.Readings),
						len(before.Events), len(after.Events), len(before.Ledger.Segments), len(after.Ledger.Segments))
				}
			},
		},
		{
			name: "one operation mutation does not block another operation",
			run: func(t *testing.T) {
				service, observed := cancellationTestService(t, at)
				busyID := cancellationCreateAndSeal(t, service, at, "busy-bin")
				otherID := cancellationCreateAndSeal(t, service, at, "other-bin")
				proceed := make(chan struct{})
				ctx, cancel := context.WithCancel(context.Background())
				observed.target = busyID
				observed.proceed = proceed

				busyDone := make(chan error, 1)
				go func() {
					_, submitErr := service.SubmitReadings(ctx, busyID, app.ReadingsRequest{Readings: replay.ExposureReadings(at, 1, false)})
					busyDone <- submitErr
				}()
				select {
				case <-observed.entered:
				case <-time.After(2 * time.Second):
					t.Fatal("busy operation did not enter its mutation")
				}

				getDone := make(chan error, 1)
				updateDone := make(chan error, 1)
				go func() {
					_, getErr := service.Get(context.Background(), otherID)
					getDone <- getErr
				}()
				go func() {
					raws := []domain.RawReading{
						{EventID: "good", ProbeID: "p-n-1", Sequence: 1, DeviceTime: at, ReceivedAt: at, Value: 320, Unit: "ppm"},
						{EventID: "bad", ProbeID: "p-s-1", Sequence: 2, DeviceTime: at, ReceivedAt: at, Value: 1, Unit: "unknown"},
						{EventID: "good", ProbeID: "p-n-1", Sequence: 1, DeviceTime: at, ReceivedAt: at, Value: 320, Unit: "ppm"},
					}
					response, updateErr := service.SubmitReadings(context.Background(), otherID, app.ReadingsRequest{Readings: raws})
					if updateErr == nil && (len(response.Results) != 3 || response.Results[0].Disposition != domain.ReadingAccepted || response.Results[1].Disposition != domain.ReadingRejected || response.Results[2].Disposition != domain.ReadingDuplicate) {
						updateErr = fmt.Errorf("unexpected per-reading results: %#v", response.Results)
					}
					updateDone <- updateErr
				}()

				deadline := time.After(750 * time.Millisecond)
				var getErr, updateErr error
				completed := 0
				timedOut := false
				for completed < 2 {
					select {
					case getErr = <-getDone:
						getDone = nil
						completed++
					case updateErr = <-updateDone:
						updateDone = nil
						completed++
					case <-deadline:
						timedOut = true
					}
					if timedOut {
						break
					}
				}
				cancel()
				close(proceed)
				<-busyDone
				for completed < 2 {
					select {
					case getErr = <-getDone:
						getDone = nil
						completed++
					case updateErr = <-updateDone:
						updateDone = nil
						completed++
					case <-time.After(3 * time.Second):
						t.Fatal("unrelated operation did not finish after batch cleanup")
					}
				}
				if timedOut {
					t.Fatal("an unrelated operation remained blocked by another operation's batch")
				}
				if getErr != nil || updateErr != nil {
					t.Fatalf("unrelated operation failed: get=%v update=%v", getErr, updateErr)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}
