package replay

import (
	"context"
	"fmt"
	"time"

	"grain-fumigation-interlock/internal/app"
	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/ventilation"
)

type FixedClock struct {
	T time.Time
}

func (c *FixedClock) Now() time.Time { return c.T }

func RunDeterministic(ctx context.Context, svc *app.Service, anchor time.Time) (string, error) {
	create, err := svc.CreateOperation(ctx, SampleCreate(anchor))
	if err != nil {
		return "", err
	}
	if _, err := svc.Seal(ctx, create.ID, app.SealRequest{ExpectedRevision: create.Revision, SealedAt: anchor}); err != nil {
		return "", err
	}
	if _, err := svc.SubmitReadings(ctx, create.ID, app.ReadingsRequest{Readings: ExposureReadings(anchor, 95, false)}); err != nil {
		return "", err
	}
	status, err := svc.Get(ctx, create.ID)
	if err != nil {
		return "", err
	}
	if status.Operation.Status != domain.StatusReadyToVent {
		return "", fmt.Errorf("operation did not reach ventilation readiness: %s", status.Operation.Status)
	}
	for _, stage := range domain.VentilationPlan {
		current, err := svc.Get(ctx, create.ID)
		if err != nil {
			return "", err
		}
		_, err = svc.VentilationCommand(ctx, create.ID, app.VentilationCommandRequest{Idempotency: string(stage), Stage: stage, ExpectedRevision: current.Operation.Revision})
		if err != nil {
			return "", err
		}
	}
	if _, err := svc.EntryPermit(ctx, create.ID, ventilation.EntryRequest{RequestID: "entry-1", Idempotency: "entry-1", Operator: "smoke"}); err != nil {
		return "", err
	}
	resp, err := svc.Archive(ctx, create.ID)
	if err != nil {
		return "", err
	}
	return resp.Manifest.ID, nil
}
