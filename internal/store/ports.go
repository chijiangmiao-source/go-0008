package store

import (
	"context"

	"grain-fumigation-interlock/internal/domain"
)

type EventStorePort interface {
	Create(ctx context.Context, snapshot domain.OperationSnapshot, events []domain.EventRecord) error
	Get(ctx context.Context, id string) (domain.OperationSnapshot, error)
	Update(ctx context.Context, id string, expectedRevision int64, mutate func(*domain.OperationSnapshot) ([]domain.EventRecord, error)) (domain.OperationSnapshot, error)
	List(ctx context.Context) ([]domain.OperationSnapshot, error)
	AppendOutbox(ctx context.Context, intent domain.ControlIntent) error
	MarkOutbox(ctx context.Context, commandID string, ack domain.ControlAck) error
	PendingOutbox(ctx context.Context) ([]domain.ControlIntent, error)
	SaveArchive(ctx context.Context, manifest domain.ArchiveManifest) error
}
