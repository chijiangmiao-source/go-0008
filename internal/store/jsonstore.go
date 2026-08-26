package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"grain-fumigation-interlock/internal/domain"
)

type JSONStore struct {
	root      string
	mu        sync.Mutex
	snapshots map[string]domain.OperationSnapshot
	events    map[string][]domain.EventRecord
	outbox    map[string]domain.ControlIntent
	archives  map[string]domain.ArchiveManifest
	nextEvent int64
}

func OpenJSONStore(root string) (*JSONStore, error) {
	if root == "" {
		return nil, domain.ValidationError{Code: "store_root_required", Message: "store root is required"}
	}
	s := &JSONStore{
		root:      root,
		snapshots: map[string]domain.OperationSnapshot{},
		events:    map[string][]domain.EventRecord{},
		outbox:    map[string]domain.ControlIntent{},
		archives:  map[string]domain.ArchiveManifest{},
	}
	for _, dir := range []string{s.snapDir(), s.eventDir(), s.outboxDir(), s.archiveDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	if err := s.loadSnapshots(); err != nil {
		return nil, err
	}
	if err := s.loadEvents(); err != nil {
		return nil, err
	}
	if err := s.loadOutbox(); err != nil {
		return nil, err
	}
	if err := s.loadArchives(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *JSONStore) Create(ctx context.Context, snapshot domain.OperationSnapshot, events []domain.EventRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	id := snapshot.Operation.ID
	if id == "" {
		return domain.ValidationError{Code: "operation_id_required", Message: "operation id is required"}
	}
	if _, exists := s.snapshots[id]; exists {
		return domain.ConflictError{Code: "operation_exists", Message: id}
	}
	enriched := s.prepareEvents(id, snapshot.Operation.Revision, events)
	snapshot.Events = append(snapshot.Events, enriched...)
	s.snapshots[id] = cloneSnapshot(snapshot)
	s.events[id] = append([]domain.EventRecord(nil), enriched...)
	if err := s.persistSnapshot(id); err != nil {
		delete(s.snapshots, id)
		delete(s.events, id)
		return err
	}
	if err := s.persistEvents(id); err != nil {
		return err
	}
	return nil
}

func (s *JSONStore) Get(ctx context.Context, id string) (domain.OperationSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.OperationSnapshot{}, err
	}
	snap, ok := s.snapshots[id]
	if !ok {
		return domain.OperationSnapshot{}, domain.NotFoundError{Resource: "operation", ID: id}
	}
	cp := cloneSnapshot(snap)
	cp.Events = append([]domain.EventRecord(nil), s.events[id]...)
	return cp, nil
}

func (s *JSONStore) Update(ctx context.Context, id string, expectedRevision int64, mutate func(*domain.OperationSnapshot) ([]domain.EventRecord, error)) (domain.OperationSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.OperationSnapshot{}, err
	}
	current, ok := s.snapshots[id]
	if !ok {
		return domain.OperationSnapshot{}, domain.NotFoundError{Resource: "operation", ID: id}
	}
	if expectedRevision >= 0 && current.Operation.Revision != expectedRevision {
		return domain.OperationSnapshot{}, domain.ConflictError{Code: "revision_conflict", Message: fmt.Sprintf("expected %d got %d", expectedRevision, current.Operation.Revision)}
	}
	working := cloneSnapshot(current)
	events, err := mutate(&working)
	if err != nil {
		return domain.OperationSnapshot{}, err
	}
	if working.Operation.Revision <= current.Operation.Revision {
		working.Operation.Revision = current.Operation.Revision + 1
	}
	working.Operation.UpdatedAt = time.Now().UTC()
	enriched := s.prepareEvents(id, working.Operation.Revision, events)
	working.Events = append([]domain.EventRecord(nil), s.events[id]...)
	working.Events = append(working.Events, enriched...)
	s.snapshots[id] = cloneSnapshot(working)
	s.events[id] = append(s.events[id], enriched...)
	if err := s.persistSnapshot(id); err != nil {
		s.snapshots[id] = current
		s.events[id] = s.events[id][:len(s.events[id])-len(enriched)]
		return domain.OperationSnapshot{}, err
	}
	if err := s.persistEvents(id); err != nil {
		return domain.OperationSnapshot{}, err
	}
	return cloneSnapshot(working), nil
}

func (s *JSONStore) List(ctx context.Context) ([]domain.OperationSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(s.snapshots))
	for id := range s.snapshots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]domain.OperationSnapshot, 0, len(ids))
	for _, id := range ids {
		result = append(result, cloneSnapshot(s.snapshots[id]))
	}
	return result, nil
}

func (s *JSONStore) AppendOutbox(ctx context.Context, intent domain.ControlIntent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	existing, ok := s.outbox[intent.CommandID]
	if ok {
		if existing.Idempotency == intent.Idempotency && existing.OperationID == intent.OperationID && existing.Stage == intent.Stage {
			return nil
		}
		return domain.ConflictError{Code: "command_conflict", Message: intent.CommandID}
	}
	intent.Status = domain.CommandPending
	s.outbox[intent.CommandID] = intent
	return s.persistOutbox(intent.CommandID)
}

func (s *JSONStore) MarkOutbox(ctx context.Context, commandID string, ack domain.ControlAck) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	intent, ok := s.outbox[commandID]
	if !ok {
		return domain.NotFoundError{Resource: "command", ID: commandID}
	}
	now := ack.ReceivedAt
	intent.DeliveredAt = &now
	if ack.Accepted {
		intent.Status = domain.CommandAcked
		intent.LastError = ""
	} else {
		intent.Status = domain.CommandFailed
		intent.LastError = ack.Message
	}
	s.outbox[commandID] = intent
	return s.persistOutbox(commandID)
}

func (s *JSONStore) PendingOutbox(ctx context.Context) ([]domain.ControlIntent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var result []domain.ControlIntent
	for _, intent := range s.outbox {
		if intent.Status == domain.CommandPending {
			result = append(result, intent)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, nil
}

func (s *JSONStore) SaveArchive(ctx context.Context, manifest domain.ArchiveManifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.archives[manifest.ID] = manifest
	return writeJSONAtomic(s.archivePath(manifest.ID), manifest)
}

func (s *JSONStore) prepareEvents(id string, version int64, events []domain.EventRecord) []domain.EventRecord {
	now := time.Now().UTC()
	out := make([]domain.EventRecord, len(events))
	for i, ev := range events {
		s.nextEvent++
		ev.Sequence = s.nextEvent
		ev.AggregateID = id
		ev.Version = version
		if ev.OccurredAt.IsZero() {
			ev.OccurredAt = now
		}
		out[i] = ev
	}
	return out
}

func (s *JSONStore) loadSnapshots() error {
	entries, err := os.ReadDir(s.snapDir())
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var snap domain.OperationSnapshot
		if err := readJSON(filepath.Join(s.snapDir(), e.Name()), &snap); err != nil {
			return err
		}
		s.snapshots[snap.Operation.ID] = snap
	}
	return nil
}

func (s *JSONStore) loadEvents() error {
	entries, err := os.ReadDir(s.eventDir())
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var evs []domain.EventRecord
		if err := readJSON(filepath.Join(s.eventDir(), e.Name()), &evs); err != nil {
			return err
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		s.events[id] = evs
		for _, ev := range evs {
			if ev.Sequence > s.nextEvent {
				s.nextEvent = ev.Sequence
			}
		}
	}
	return nil
}

func (s *JSONStore) loadOutbox() error {
	entries, err := os.ReadDir(s.outboxDir())
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var intent domain.ControlIntent
		if err := readJSON(filepath.Join(s.outboxDir(), e.Name()), &intent); err != nil {
			return err
		}
		s.outbox[intent.CommandID] = intent
	}
	return nil
}

func (s *JSONStore) loadArchives() error {
	entries, err := os.ReadDir(s.archiveDir())
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var manifest domain.ArchiveManifest
		if err := readJSON(filepath.Join(s.archiveDir(), e.Name()), &manifest); err != nil {
			return err
		}
		s.archives[manifest.ID] = manifest
	}
	return nil
}

func (s *JSONStore) persistSnapshot(id string) error {
	return writeJSONAtomic(s.snapshotPath(id), s.snapshots[id])
}
func (s *JSONStore) persistEvents(id string) error {
	return writeJSONAtomic(s.eventPath(id), s.events[id])
}
func (s *JSONStore) persistOutbox(id string) error {
	return writeJSONAtomic(s.outboxPath(id), s.outbox[id])
}

func (s *JSONStore) snapDir() string               { return filepath.Join(s.root, "snapshots") }
func (s *JSONStore) eventDir() string              { return filepath.Join(s.root, "events") }
func (s *JSONStore) outboxDir() string             { return filepath.Join(s.root, "outbox") }
func (s *JSONStore) archiveDir() string            { return filepath.Join(s.root, "archives") }
func (s *JSONStore) snapshotPath(id string) string { return filepath.Join(s.snapDir(), id+".json") }
func (s *JSONStore) eventPath(id string) string    { return filepath.Join(s.eventDir(), id+".json") }
func (s *JSONStore) outboxPath(id string) string   { return filepath.Join(s.outboxDir(), id+".json") }
func (s *JSONStore) archivePath(id string) string  { return filepath.Join(s.archiveDir(), id+".json") }

func writeJSONAtomic(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, value interface{}) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, value)
}

func cloneSnapshot(in domain.OperationSnapshot) domain.OperationSnapshot {
	data, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	var out domain.OperationSnapshot
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return out
}
