package domain

import "time"

type VentilationStage string

const (
	StageNone      VentilationStage = ""
	StagePurge     VentilationStage = "purge"
	StageDilution  VentilationStage = "dilution"
	StageClearance VentilationStage = "clearance"
	StageComplete  VentilationStage = "complete"
)

var VentilationPlan = []VentilationStage{StagePurge, StageDilution, StageClearance}

type CommandStatus string

const (
	CommandPending  CommandStatus = "pending"
	CommandAcked    CommandStatus = "acked"
	CommandFailed   CommandStatus = "failed"
	CommandCanceled CommandStatus = "canceled"
)

type VentilationExecution struct {
	Plan               []VentilationStage `json:"plan"`
	ActiveStage        VentilationStage   `json:"active_stage,omitempty"`
	CompletedStages    []VentilationStage `json:"completed_stages"`
	StageDeadline      *time.Time         `json:"stage_deadline,omitempty"`
	CommandID          string             `json:"command_id,omitempty"`
	Ack                *ControlAck        `json:"ack,omitempty"`
	MutexToken         string             `json:"mutex_token,omitempty"`
	AbortReason        string             `json:"abort_reason,omitempty"`
	ResetRecords       []ResetRecord      `json:"reset_records,omitempty"`
	LastCommandRequest string             `json:"last_command_request,omitempty"`
}

type ControlIntent struct {
	CommandID   string           `json:"command_id"`
	OperationID string           `json:"operation_id"`
	Stage       VentilationStage `json:"stage"`
	Idempotency string           `json:"idempotency"`
	Expected    VentilationStage `json:"expected_stage"`
	CreatedAt   time.Time        `json:"created_at"`
	Status      CommandStatus    `json:"status"`
	LastError   string           `json:"last_error,omitempty"`
	DeliveredAt *time.Time       `json:"delivered_at,omitempty"`
}

type ControlAck struct {
	CommandID   string           `json:"command_id"`
	Stage       VentilationStage `json:"stage"`
	Accepted    bool             `json:"accepted"`
	Controller  string           `json:"controller"`
	ReceivedAt  time.Time        `json:"received_at"`
	Message     string           `json:"message,omitempty"`
	PhysicalRun bool             `json:"physical_run"`
}

type ResetRecord struct {
	Reason        string          `json:"reason"`
	VerifiedAt    time.Time       `json:"verified_at"`
	VerifiedBy    string          `json:"verified_by"`
	RestoredState OperationStatus `json:"restored_state"`
}

type EntryPermit struct {
	RequestID         string      `json:"request_id"`
	ConditionSnapshot string      `json:"condition_snapshot"`
	UnlockCommandID   string      `json:"unlock_command_id"`
	Ack               *ControlAck `json:"ack,omitempty"`
	CommittedAt       *time.Time  `json:"committed_at,omitempty"`
	PostUnlockRisk    string      `json:"post_unlock_risk,omitempty"`
}

func (v VentilationExecution) NextStage() VentilationStage {
	completed := map[VentilationStage]bool{}
	for _, s := range v.CompletedStages {
		completed[s] = true
	}
	for _, s := range VentilationPlan {
		if !completed[s] {
			return s
		}
	}
	return StageComplete
}

func (v VentilationExecution) StageComplete(stage VentilationStage) bool {
	for _, s := range v.CompletedStages {
		if s == stage {
			return true
		}
	}
	return false
}
