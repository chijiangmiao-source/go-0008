package app

import (
	"time"

	"grain-fumigation-interlock/internal/domain"
	"grain-fumigation-interlock/internal/ventilation"
)

type CreateOperationRequest struct {
	Organization      string                     `json:"organization"`
	Site              string                     `json:"site"`
	Carrier           domain.CarrierType         `json:"carrier"`
	Agent             string                     `json:"agent"`
	DoseGramsPerTonne float64                    `json:"dose_grams_per_tonne"`
	PlannedSealTime   time.Time                  `json:"planned_seal_time"`
	Areas             []domain.AreaConfig        `json:"areas"`
	Coverage          domain.CoverageRule        `json:"coverage"`
	Probes            []domain.ProbeRegistration `json:"probes"`
	ExternalReference string                     `json:"external_reference"`
}

type CreateOperationResponse struct {
	ID              string                       `json:"id"`
	Status          domain.OperationStatus       `json:"status"`
	Revision        int64                        `json:"revision"`
	StandardVersion domain.SafetyStandardVersion `json:"standard"`
}

type SealRequest struct {
	ExpectedRevision int64     `json:"expected_revision"`
	SealedAt         time.Time `json:"sealed_at"`
}

type ReadingsRequest struct {
	Readings []domain.RawReading `json:"readings"`
}

type ReadingsResponse struct {
	Results           []domain.ReadingResult `json:"results"`
	Status            domain.OperationStatus `json:"status"`
	Revision          int64                  `json:"revision"`
	Ledger            domain.ExposureLedger  `json:"ledger"`
	CreatedDeviations []domain.DeviationCase `json:"created_deviations"`
}

type OperationStatusResponse struct {
	Operation     domain.FumigationOperation    `json:"operation"`
	Ledger        domain.ExposureLedger         `json:"ledger"`
	Coverage      map[string]int                `json:"coverage"`
	ProbeHealth   map[string]domain.ProbeHealth `json:"probe_health"`
	OpenDeviation []domain.DeviationCase        `json:"open_deviation"`
	Ventilation   domain.VentilationExecution   `json:"ventilation"`
	Entry         domain.EntryPermit            `json:"entry"`
	Archive       *domain.ArchiveManifest       `json:"archive,omitempty"`
}

type VentilationCommandRequest = ventilation.CommandRequest
type EntryRequest = ventilation.EntryRequest

type EmergencyStopRequest struct {
	Reason string `json:"reason"`
}

type ResetRequest struct {
	Reason                string `json:"reason"`
	VerifiedBy            string `json:"verified_by"`
	FreshReadingsVerified bool   `json:"fresh_readings_verified"`
}

type ArchiveResponse struct {
	Manifest domain.ArchiveManifest `json:"manifest"`
	Status   domain.OperationStatus `json:"status"`
	Revision int64                  `json:"revision"`
}
