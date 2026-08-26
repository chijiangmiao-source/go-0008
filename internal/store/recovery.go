package store

import (
	"context"
	"sort"

	"grain-fumigation-interlock/internal/domain"
)

type RecoveryReport struct {
	Operations         int      `json:"operations"`
	PendingCommands    int      `json:"pending_commands"`
	RecoveredOperation []string `json:"recovered_operation"`
}

func Recover(ctx context.Context, port EventStorePort) (RecoveryReport, error) {
	snaps, err := port.List(ctx)
	if err != nil {
		return RecoveryReport{}, err
	}
	pending, err := port.PendingOutbox(ctx)
	if err != nil {
		return RecoveryReport{}, err
	}
	report := RecoveryReport{Operations: len(snaps), PendingCommands: len(pending)}
	for _, snap := range snaps {
		if snap.Operation.Status != domain.StatusArchived {
			report.RecoveredOperation = append(report.RecoveredOperation, snap.Operation.ID)
		}
	}
	sort.Strings(report.RecoveredOperation)
	return report, nil
}
