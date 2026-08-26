package app

import (
	"context"
	"time"

	"grain-fumigation-interlock/internal/archive"
	"grain-fumigation-interlock/internal/deviation"
	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/exposure"
	"grain-fumigation-interlock/internal/sensor"
	"grain-fumigation-interlock/internal/store"
	"grain-fumigation-interlock/internal/ventilation"
)

type Service struct {
	store      store.EventStorePort
	standards  domain.Standards
	guard      domain.TransitionGuard
	clock      domain.Clock
	ids        *IDFactory
	receiver   sensor.Receiver
	calculator exposure.Calculator
	deviations deviation.Machine
	interlock  ventilation.Interlock
	entry      ventilation.EntryService
	archiver   archive.Builder
}

func NewService(port store.EventStorePort, standards domain.Standards, clock domain.Clock, controller ventilation.VentilationControllerPort) *Service {
	if clock == nil {
		clock = domain.SystemClock{}
	}
	if controller == nil {
		controller = ventilation.NewSimulatedController()
	}
	return &Service{
		store: port, standards: standards, guard: domain.NewTransitionGuard(), clock: clock, ids: &IDFactory{},
		receiver: sensor.NewReceiver(standards, 10*time.Minute), calculator: exposure.NewCalculator(standards),
		interlock: ventilation.Interlock{Controller: controller}, entry: ventilation.EntryService{Controller: controller},
		archiver: archive.Builder{Standards: standards},
	}
}

func (s *Service) CreateOperation(ctx context.Context, req CreateOperationRequest) (CreateOperationResponse, error) {
	now := s.clock.Now()
	if req.PlannedSealTime.IsZero() {
		req.PlannedSealTime = now
	}
	std, err := s.standards.ForRegistration(now)
	if err != nil {
		return CreateOperationResponse{}, err
	}
	op := domain.FumigationOperation{
		ID: s.ids.OperationID(req.Site, now), Organization: req.Organization, Site: req.Site, Carrier: req.Carrier,
		Agent: req.Agent, DoseGramsPerTonne: req.DoseGramsPerTonne, PlannedSealTime: req.PlannedSealTime.UTC(),
		Areas: req.Areas, Coverage: req.Coverage, Probes: req.Probes, Status: domain.StatusRegistered,
		Revision: 1, StandardVersion: std.Version, CreatedAt: now, UpdatedAt: now, ExternalReferenceID: req.ExternalReference,
	}
	if op.Coverage.MaxGap <= 0 {
		op.Coverage.MaxGap = std.MaxSampleGap
	}
	if op.Coverage.LateWindow <= 0 {
		op.Coverage.LateWindow = std.LateWindow
	}
	for i := range op.Probes {
		if op.Probes[i].Health == "" {
			op.Probes[i].Health = domain.ProbeHealthy
		}
		if op.Probes[i].EnabledAt.IsZero() {
			op.Probes[i].EnabledAt = now
		}
	}
	if err := op.ValidateForCreate(); err != nil {
		return CreateOperationResponse{}, err
	}
	snap := domain.OperationSnapshot{
		Operation:   op,
		Ledger:      domain.ExposureLedger{LastCoverageByArea: map[string]int{}},
		Ventilation: domain.VentilationExecution{Plan: domain.VentilationPlan},
	}
	ev := domain.EventRecord{Type: "operation.registered", Payload: map[string]interface{}{"standard": std.Version}, OccurredAt: now}
	if err := s.store.Create(ctx, snap, []domain.EventRecord{ev}); err != nil {
		return CreateOperationResponse{}, err
	}
	return CreateOperationResponse{ID: op.ID, Status: op.Status, Revision: op.Revision, StandardVersion: std}, nil
}

func (s *Service) Seal(ctx context.Context, id string, req SealRequest) (domain.OperationSnapshot, error) {
	now := s.clock.Now()
	return s.store.Update(ctx, id, req.ExpectedRevision, func(snap *domain.OperationSnapshot) ([]domain.EventRecord, error) {
		if err := s.guard.Require(snap.Operation.Status, domain.StatusSealed); err != nil {
			return nil, err
		}
		when := req.SealedAt
		if when.IsZero() {
			when = now
		}
		snap.Operation.SealedAt = &when
		snap.Operation.Status = domain.StatusSealed
		snap.Operation.LastAuditMessage = "seal confirmed, monitoring may begin"
		if err := s.guard.Require(snap.Operation.Status, domain.StatusExposing); err != nil {
			return nil, err
		}
		snap.Operation.Status = domain.StatusExposing
		return []domain.EventRecord{{Type: "operation.sealed", Payload: req, OccurredAt: now}}, nil
	})
}

func (s *Service) SubmitReadings(ctx context.Context, id string, req ReadingsRequest) (ReadingsResponse, error) {
	now := s.clock.Now()
	var response ReadingsResponse
	snap, err := s.store.Update(ctx, id, -1, func(snap *domain.OperationSnapshot) ([]domain.EventRecord, error) {
		if !snap.Operation.Status.AllowsMonitoring() {
			return nil, domain.ConflictError{Code: "operation_not_monitoring", Message: string(snap.Operation.Status)}
		}
		out, sensorDevs := s.receiver.Process(ctx, snap, req.Readings)
		ledger, exposureDevs := s.calculator.Recalculate(snap.Operation, snap.Readings)
		snap.Ledger = ledger
		devs := append(sensorDevs, exposureDevs...)
		added := s.deviations.AddCases(snap, devs, now)
		if snap.OpenDeviationCount() == 0 && snap.Operation.Status == domain.StatusExposing && snap.Ledger.RequirementSatisfiedAt != nil {
			if err := s.guard.Require(snap.Operation.Status, domain.StatusReadyToVent); err != nil {
				return nil, err
			}
			snap.Operation.Status = domain.StatusReadyToVent
		}
		response = ReadingsResponse{Results: out.Results, Status: snap.Operation.Status, Revision: snap.Operation.Revision + 1, Ledger: snap.Ledger, CreatedDeviations: devs}
		events := []domain.EventRecord{{Type: "sensor.batch_processed", Payload: out.Results, OccurredAt: now}, {Type: "exposure.recalculated", Payload: snap.Ledger, OccurredAt: now}}
		if added {
			events = append(events, domain.EventRecord{Type: "deviation.opened", Payload: devs, OccurredAt: now})
		}
		return events, nil
	})
	if err != nil {
		return ReadingsResponse{}, err
	}
	response.Status = snap.Operation.Status
	response.Revision = snap.Operation.Revision
	response.Ledger = snap.Ledger
	return response, nil
}

func (s *Service) Get(ctx context.Context, id string) (OperationStatusResponse, error) {
	snap, err := s.store.Get(ctx, id)
	if err != nil {
		return OperationStatusResponse{}, err
	}
	health := map[string]domain.ProbeHealth{}
	for _, p := range snap.Operation.Probes {
		health[p.ID] = p.Health
	}
	var open []domain.DeviationCase
	for _, d := range snap.Deviations {
		if d.IsOpen() {
			open = append(open, d)
		}
	}
	return OperationStatusResponse{Operation: snap.Operation, Ledger: snap.Ledger, Coverage: snap.Operation.HealthyCoverage(), ProbeHealth: health, OpenDeviation: open, Ventilation: snap.Ventilation, Entry: snap.Entry, Archive: snap.Archive}, nil
}

func (s *Service) Evidence(ctx context.Context, id string, offset, limit int) (domain.EvidencePage, error) {
	snap, err := s.store.Get(ctx, id)
	if err != nil {
		return domain.EvidencePage{}, err
	}
	var items []interface{}
	for _, ev := range snap.Events {
		items = append(items, ev)
	}
	for _, r := range snap.Readings {
		items = append(items, r)
	}
	for _, seg := range snap.Ledger.Segments {
		items = append(items, seg)
	}
	for _, d := range snap.Deviations {
		items = append(items, d)
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	next := end
	if next >= len(items) {
		next = 0
	}
	return domain.EvidencePage{Items: items[offset:end], NextOffset: next, Total: len(items)}, nil
}

func (s *Service) ResolveDeviation(ctx context.Context, id, deviationID string, req domain.ResolveDeviationRequest) (domain.OperationSnapshot, error) {
	now := s.clock.Now()
	return s.store.Update(ctx, id, -1, func(snap *domain.OperationSnapshot) ([]domain.EventRecord, error) {
		if err := s.deviations.Resolve(snap, deviationID, req, now); err != nil {
			return nil, err
		}
		return []domain.EventRecord{{Type: "deviation.closed", Payload: map[string]string{"id": deviationID}, OccurredAt: now}}, nil
	})
}

func (s *Service) VentilationCommand(ctx context.Context, id string, req VentilationCommandRequest) (domain.OperationSnapshot, error) {
	now := s.clock.Now()
	return s.store.Update(ctx, id, req.ExpectedRevision, func(snap *domain.OperationSnapshot) ([]domain.EventRecord, error) {
		ack, intent, err := s.interlock.StartOrAdvance(ctx, snap, req, now)
		if err != nil {
			if _, ok := err.(domain.BoundaryError); ok {
				s.deviations.AddCases(snap, []domain.DeviationCase{{ID: "control-" + intent.CommandID, Type: domain.DeviationControl, Severity: domain.SeverityHigh, OriginalSafeStatus: snap.Operation.Status, Status: domain.DeviationOpen, TriggerEvidence: []string{intent.CommandID}, OpenedAt: now}}, now)
			}
			return nil, err
		}
		return []domain.EventRecord{{Type: "ventilation.command_acked", Payload: ack, OccurredAt: now}}, nil
	})
}

func (s *Service) EmergencyStop(ctx context.Context, id string, req EmergencyStopRequest) (domain.OperationSnapshot, error) {
	now := s.clock.Now()
	return s.store.Update(ctx, id, -1, func(snap *domain.OperationSnapshot) ([]domain.EventRecord, error) {
		if err := s.interlock.EmergencyStop(snap, req.Reason, now); err != nil {
			return nil, err
		}
		return []domain.EventRecord{{Type: "operation.emergency_stop", Payload: req, OccurredAt: now}}, nil
	})
}

func (s *Service) Reset(ctx context.Context, id string, req ResetRequest) (domain.OperationSnapshot, error) {
	now := s.clock.Now()
	return s.store.Update(ctx, id, -1, func(snap *domain.OperationSnapshot) ([]domain.EventRecord, error) {
		if err := s.interlock.Reset(snap, req.Reason, req.VerifiedBy, req.FreshReadingsVerified, now); err != nil {
			return nil, err
		}
		return []domain.EventRecord{{Type: "operation.reset", Payload: req, OccurredAt: now}}, nil
	})
}

func (s *Service) EntryPermit(ctx context.Context, id string, req EntryRequest) (domain.OperationSnapshot, error) {
	now := s.clock.Now()
	return s.store.Update(ctx, id, -1, func(snap *domain.OperationSnapshot) ([]domain.EventRecord, error) {
		permit, err := s.entry.Request(ctx, snap, req, now)
		if err != nil {
			return nil, err
		}
		return []domain.EventRecord{{Type: "entry.unlocked", Payload: permit, OccurredAt: now}}, nil
	})
}

func (s *Service) Archive(ctx context.Context, id string) (ArchiveResponse, error) {
	now := s.clock.Now()
	var manifest domain.ArchiveManifest
	snap, err := s.store.Update(ctx, id, -1, func(snap *domain.OperationSnapshot) ([]domain.EventRecord, error) {
		var err error
		manifest, err = s.archiver.Build(*snap, now)
		if err != nil {
			return nil, err
		}
		if err := s.guard.Require(snap.Operation.Status, domain.StatusArchived); err != nil {
			return nil, err
		}
		snap.Archive = &manifest
		snap.Operation.Status = domain.StatusArchived
		snap.Operation.ArchivedManifestID = manifest.ID
		return []domain.EventRecord{{Type: "archive.created", Payload: manifest, OccurredAt: now}}, nil
	})
	if err != nil {
		return ArchiveResponse{}, err
	}
	if err := s.store.SaveArchive(ctx, manifest); err != nil {
		return ArchiveResponse{}, err
	}
	return ArchiveResponse{Manifest: manifest, Status: snap.Operation.Status, Revision: snap.Operation.Revision}, nil
}
