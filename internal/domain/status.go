package domain

import (
	"fmt"
	"time"
)

type OperationStatus string

const (
	StatusRegistered    OperationStatus = "registered"
	StatusSealed        OperationStatus = "sealed"
	StatusExposing      OperationStatus = "exposing"
	StatusDeviation     OperationStatus = "deviation"
	StatusReadyToVent   OperationStatus = "ready_to_vent"
	StatusVentilating   OperationStatus = "ventilating"
	StatusEmergencyStop OperationStatus = "emergency_stop"
	StatusEntryUnlocked OperationStatus = "entry_unlocked"
	StatusArchived      OperationStatus = "archived"
)

func (s OperationStatus) IsTerminal() bool { return s == StatusArchived }

func (s OperationStatus) AllowsMonitoring() bool {
	return s == StatusSealed || s == StatusExposing || s == StatusReadyToVent || s == StatusVentilating || s == StatusDeviation || s == StatusEmergencyStop
}

func (s OperationStatus) AllowsControl() bool {
	return s == StatusReadyToVent || s == StatusVentilating || s == StatusDeviation || s == StatusEmergencyStop
}

type TransitionGuard struct {
	from map[OperationStatus]map[OperationStatus]bool
}

func NewTransitionGuard() TransitionGuard {
	return TransitionGuard{from: map[OperationStatus]map[OperationStatus]bool{
		StatusRegistered:    {StatusSealed: true},
		StatusSealed:        {StatusExposing: true, StatusDeviation: true, StatusEmergencyStop: true},
		StatusExposing:      {StatusReadyToVent: true, StatusDeviation: true, StatusEmergencyStop: true},
		StatusDeviation:     {StatusExposing: true, StatusReadyToVent: true, StatusVentilating: true, StatusEmergencyStop: true},
		StatusReadyToVent:   {StatusVentilating: true, StatusEmergencyStop: true},
		StatusVentilating:   {StatusReadyToVent: true, StatusEntryUnlocked: true, StatusDeviation: true, StatusEmergencyStop: true},
		StatusEmergencyStop: {StatusExposing: true, StatusReadyToVent: true, StatusVentilating: true},
		StatusEntryUnlocked: {StatusArchived: true},
		StatusArchived:      {},
	}}
}

func (g TransitionGuard) CanMove(from, to OperationStatus) bool {
	return g.from[from][to]
}

func (g TransitionGuard) Require(from, to OperationStatus) error {
	if g.CanMove(from, to) {
		return nil
	}
	return ConflictError{Code: "illegal_transition", Message: fmt.Sprintf("cannot move from %s to %s", from, to)}
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }
