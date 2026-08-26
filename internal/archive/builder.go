package archive

import (
	"time"

	"grain-fumigation-interlock/internal/domain"
)

type Builder struct {
	Standards domain.Standards
}

func (b Builder) Build(snapshot domain.OperationSnapshot, now time.Time) (domain.ArchiveManifest, error) {
	if snapshot.Operation.Status != domain.StatusEntryUnlocked {
		return domain.ArchiveManifest{}, domain.ConflictError{Code: "archive_requires_unlocked_entry", Message: string(snapshot.Operation.Status)}
	}
	if snapshot.Entry.CommittedAt == nil || snapshot.Entry.Ack == nil || !snapshot.Entry.Ack.Accepted {
		return domain.ArchiveManifest{}, domain.BoundaryError{Boundary: domain.BoundaryArchive, Code: "entry_ack_missing", Message: "physical unlock ack must be present"}
	}
	if len(snapshot.Ledger.Segments) == 0 {
		return domain.ArchiveManifest{}, domain.BoundaryError{Boundary: domain.BoundaryArchive, Code: "ledger_missing", Message: "exposure ledger is required"}
	}
	stdDigests := map[string]string{}
	for _, seg := range snapshot.Ledger.Segments {
		if seg.StandardVersion == "" {
			continue
		}
		if _, exists := stdDigests[seg.StandardVersion]; exists {
			continue
		}
		if std, ok := b.Standards.ByVersion(seg.StandardVersion); ok {
			stdDigests[std.Version] = std.Digest
		}
	}
	if len(stdDigests) == 0 {
		return domain.ArchiveManifest{}, domain.BoundaryError{Boundary: domain.BoundaryArchive, Code: "standard_snapshot_missing", Message: "at least one standard version is required"}
	}
	manifest := domain.ArchiveManifest{
		ID:              now.UTC().Format("20060102150405") + "-" + snapshot.Operation.ID,
		OperationID:     snapshot.Operation.ID,
		CreatedAt:       now.UTC(),
		ConfigDigest:    snapshot.Operation.ConfigurationDigest(),
		StandardDigests: stdDigests,
		RecordCounts: map[string]int{
			"readings":           len(snapshot.Readings),
			"segments":           len(snapshot.Ledger.Segments),
			"deviations":         len(snapshot.Deviations),
			"events":             len(snapshot.Events),
			"ventilation_stages": len(snapshot.Ventilation.CompletedStages),
		},
		IncludesEntryAck:   true,
		IncludesLedger:     true,
		IncludesDeviations: true,
	}
	if err := manifest.Seal(); err != nil {
		return domain.ArchiveManifest{}, err
	}
	return manifest, nil
}
