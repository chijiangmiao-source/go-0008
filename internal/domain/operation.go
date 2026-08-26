package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

type CarrierType string

const (
	CarrierSilo      CarrierType = "sealed_silo"
	CarrierContainer CarrierType = "container"
)

type FumigationOperation struct {
	ID                  string              `json:"id"`
	Organization        string              `json:"organization"`
	Site                string              `json:"site"`
	Carrier             CarrierType         `json:"carrier"`
	Agent               string              `json:"agent"`
	DoseGramsPerTonne   float64             `json:"dose_grams_per_tonne"`
	PlannedSealTime     time.Time           `json:"planned_seal_time"`
	SealedAt            *time.Time          `json:"sealed_at,omitempty"`
	Areas               []AreaConfig        `json:"areas"`
	Coverage            CoverageRule        `json:"coverage"`
	Probes              []ProbeRegistration `json:"probes"`
	Status              OperationStatus     `json:"status"`
	SafePreviousStatus  OperationStatus     `json:"safe_previous_status,omitempty"`
	Revision            int64               `json:"revision"`
	StandardVersion     string              `json:"standard_version"`
	CreatedAt           time.Time           `json:"created_at"`
	UpdatedAt           time.Time           `json:"updated_at"`
	LastAuditMessage    string              `json:"last_audit_message,omitempty"`
	ArchivedManifestID  string              `json:"archived_manifest_id,omitempty"`
	ExternalReferenceID string              `json:"external_reference_id,omitempty"`
}

type AreaConfig struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Grain       string   `json:"grain"`
	ProbeIDs    []string `json:"probe_ids"`
	MinHealthy  int      `json:"min_healthy"`
	Description string   `json:"description,omitempty"`
}

type CoverageRule struct {
	MinHealthyPerArea int           `json:"min_healthy_per_area"`
	MaxGap            time.Duration `json:"max_gap"`
	LateWindow        time.Duration `json:"late_window"`
}

type ProbeHealth string

const (
	ProbeHealthy  ProbeHealth = "healthy"
	ProbeIsolated ProbeHealth = "isolated"
	ProbeMissing  ProbeHealth = "missing"
)

type ProbeRegistration struct {
	ID              string      `json:"id"`
	AreaID          string      `json:"area_id"`
	Range           ProbeRange  `json:"range"`
	Calibration     Calibration `json:"calibration"`
	EnabledAt       time.Time   `json:"enabled_at"`
	DisabledAt      *time.Time  `json:"disabled_at,omitempty"`
	Health          ProbeHealth `json:"health"`
	IsolationReason string      `json:"isolation_reason,omitempty"`
	LastEventID     string      `json:"last_event_id,omitempty"`
	LastSequence    int64       `json:"last_sequence,omitempty"`
	LastCorrectedAt *time.Time  `json:"last_corrected_at,omitempty"`
}

type ProbeRange struct {
	MinConcentration float64 `json:"min_concentration_ppm"`
	MaxConcentration float64 `json:"max_concentration_ppm"`
	MinTemperature   float64 `json:"min_temperature_c"`
	MaxTemperature   float64 `json:"max_temperature_c"`
	MinPressure      float64 `json:"min_pressure_kpa"`
	MaxPressure      float64 `json:"max_pressure_kpa"`
}

type Calibration struct {
	ClockOffset time.Duration `json:"clock_offset"`
	Gain        float64       `json:"gain"`
	Bias        float64       `json:"bias"`
}

func (o *FumigationOperation) ValidateForCreate() error {
	if strings.TrimSpace(o.Organization) == "" {
		return ValidationError{Code: "organization_required", Message: "organization is required"}
	}
	if strings.TrimSpace(o.Site) == "" {
		return ValidationError{Code: "site_required", Message: "site is required"}
	}
	if o.Carrier != CarrierSilo && o.Carrier != CarrierContainer {
		return ValidationError{Code: "carrier_invalid", Message: "carrier must be sealed_silo or container"}
	}
	if strings.TrimSpace(o.Agent) == "" {
		return ValidationError{Code: "agent_required", Message: "fumigant agent is required"}
	}
	if o.DoseGramsPerTonne <= 0 || o.DoseGramsPerTonne > 5000 {
		return ValidationError{Code: "dose_invalid", Message: "dose must be positive and inside the equipment policy"}
	}
	if len(o.Areas) == 0 {
		return ValidationError{Code: "areas_required", Message: "at least one grain area is required"}
	}
	if len(o.Probes) == 0 {
		return ValidationError{Code: "probes_required", Message: "at least one probe is required"}
	}
	if o.Coverage.MinHealthyPerArea <= 0 {
		o.Coverage.MinHealthyPerArea = 1
	}
	areaIDs := map[string]bool{}
	for _, a := range o.Areas {
		if a.ID == "" {
			return ValidationError{Code: "area_id_required", Message: "area id is required"}
		}
		if areaIDs[a.ID] {
			return ValidationError{Code: "area_duplicate", Message: "area id duplicated"}
		}
		areaIDs[a.ID] = true
		if a.MinHealthy <= 0 {
			return ValidationError{Code: "area_min_healthy_invalid", Message: "area min healthy must be positive"}
		}
	}
	probeIDs := map[string]string{}
	for _, p := range o.Probes {
		if p.ID == "" || p.AreaID == "" {
			return ValidationError{Code: "probe_required", Message: "probe id and area id are required"}
		}
		if !areaIDs[p.AreaID] {
			return ValidationError{Code: "probe_area_unknown", Message: "probe references unknown area"}
		}
		if _, exists := probeIDs[p.ID]; exists {
			return ValidationError{Code: "probe_duplicate", Message: "probe id duplicated"}
		}
		if p.Range.MaxConcentration <= p.Range.MinConcentration || p.Range.MaxTemperature <= p.Range.MinTemperature || p.Range.MaxPressure <= p.Range.MinPressure {
			return ValidationError{Code: "probe_range_invalid", Message: "probe ranges must be increasing"}
		}
		probeIDs[p.ID] = p.AreaID
	}
	for _, a := range o.Areas {
		healthy := 0
		for _, pid := range a.ProbeIDs {
			if probeIDs[pid] == a.ID {
				healthy++
			}
		}
		if healthy < a.MinHealthy {
			return ValidationError{Code: "coverage_insufficient", Message: fmt.Sprintf("area %s requires %d registered probes", a.ID, a.MinHealthy)}
		}
	}
	return nil
}

func (o FumigationOperation) ProbeByID(id string) (ProbeRegistration, bool) {
	for _, p := range o.Probes {
		if p.ID == id {
			return p, true
		}
	}
	return ProbeRegistration{}, false
}

func (o *FumigationOperation) SetProbeHealth(id string, health ProbeHealth, reason string, eventID string, seq int64, correctedAt time.Time) {
	for i := range o.Probes {
		if o.Probes[i].ID == id {
			o.Probes[i].Health = health
			o.Probes[i].IsolationReason = reason
			o.Probes[i].LastEventID = eventID
			o.Probes[i].LastSequence = seq
			o.Probes[i].LastCorrectedAt = &correctedAt
			return
		}
	}
}

func (o FumigationOperation) HealthyCoverage() map[string]int {
	result := make(map[string]int)
	for _, p := range o.Probes {
		if p.Health == "" || p.Health == ProbeHealthy {
			result[p.AreaID]++
		}
	}
	return result
}

func (o FumigationOperation) CoverageSatisfied() bool {
	healthy := o.HealthyCoverage()
	for _, a := range o.Areas {
		min := a.MinHealthy
		if min <= 0 {
			min = o.Coverage.MinHealthyPerArea
		}
		if healthy[a.ID] < min {
			return false
		}
	}
	return true
}

func (o FumigationOperation) ConfigurationDigest() string {
	parts := []string{o.Organization, o.Site, string(o.Carrier), o.Agent, o.StandardVersion}
	for _, a := range o.Areas {
		ids := append([]string(nil), a.ProbeIDs...)
		sort.Strings(ids)
		parts = append(parts, a.ID, a.Name, fmt.Sprint(a.MinHealthy), strings.Join(ids, ","))
	}
	for _, p := range o.Probes {
		parts = append(parts, p.ID, p.AreaID)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}
