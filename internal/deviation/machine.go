package deviation

import (
	"strings"
	"time"

	"grain-fumigation-interlock/internal/domain"
)

type Machine struct{}

func (Machine) AddCases(snapshot *domain.OperationSnapshot, cases []domain.DeviationCase, now time.Time) bool {
	changed := false
	for _, c := range cases {
		if c.ID == "" {
			c.ID = string(c.Type) + "-" + now.UTC().Format("20060102150405")
		}
		if containsOpen(snapshot.Deviations, c.ID) {
			continue
		}
		if c.OriginalSafeStatus == "" {
			c.OriginalSafeStatus = snapshot.Operation.Status
		}
		snapshot.Deviations = append(snapshot.Deviations, c)
		changed = true
	}
	if changed && snapshot.Operation.Status != domain.StatusDeviation && snapshot.Operation.Status != domain.StatusEmergencyStop {
		snapshot.Operation.SafePreviousStatus = snapshot.Operation.Status
		snapshot.Operation.Status = domain.StatusDeviation
	}
	return changed
}

func (Machine) Resolve(snapshot *domain.OperationSnapshot, id string, req domain.ResolveDeviationRequest, now time.Time) error {
	if strings.TrimSpace(req.Action) == "" || strings.TrimSpace(req.Review) == "" || strings.TrimSpace(req.Responsible) == "" {
		return domain.ValidationError{Code: "deviation_resolution_incomplete", Message: "action, review and responsible are required"}
	}
	found := false
	for i := range snapshot.Deviations {
		if snapshot.Deviations[i].ID == id {
			found = true
			if snapshot.Deviations[i].Status == domain.DeviationClosed {
				return domain.ConflictError{Code: "deviation_closed", Message: id}
			}
			snapshot.Deviations[i].Status = domain.DeviationClosed
			snapshot.Deviations[i].Action = req.Action
			snapshot.Deviations[i].Review = req.Review
			snapshot.Deviations[i].Responsible = req.Responsible
			t := now.UTC()
			snapshot.Deviations[i].ClosedAt = &t
		}
	}
	if !found {
		return domain.NotFoundError{Resource: "deviation", ID: id}
	}
	if snapshot.OpenDeviationCount() == 0 && snapshot.Operation.Status == domain.StatusDeviation {
		restore := snapshot.Operation.SafePreviousStatus
		if restore == "" || restore == domain.StatusDeviation {
			restore = domain.StatusExposing
		}
		if restore == domain.StatusExposing && snapshot.Ledger.RequirementSatisfiedAt != nil {
			restore = domain.StatusReadyToVent
		}
		snapshot.Operation.Status = restore
		snapshot.Operation.SafePreviousStatus = ""
	}
	return nil
}

func (Machine) CanResume(snapshot domain.OperationSnapshot) bool {
	return snapshot.OpenDeviationCount() == 0 && snapshot.Operation.CoverageSatisfied()
}

func containsOpen(cases []domain.DeviationCase, id string) bool {
	for _, c := range cases {
		if c.ID == id && c.IsOpen() {
			return true
		}
	}
	return false
}
