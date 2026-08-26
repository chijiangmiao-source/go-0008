package domain

import "fmt"

type ValidationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

type NotFoundError struct {
	Resource string
	ID       string
}

func (e NotFoundError) Error() string {
	return fmt.Sprintf("%s %s not found", e.Resource, e.ID)
}

type ConflictError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e ConflictError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

type FailureBoundary string

const (
	BoundaryInput       FailureBoundary = "input"
	BoundaryTime        FailureBoundary = "time"
	BoundaryCoverage    FailureBoundary = "coverage"
	BoundaryStandard    FailureBoundary = "standard"
	BoundaryConcurrency FailureBoundary = "concurrency"
	BoundaryControl     FailureBoundary = "control"
	BoundaryRecovery    FailureBoundary = "recovery"
	BoundaryArchive     FailureBoundary = "archive"
)

type BoundaryError struct {
	Boundary FailureBoundary `json:"boundary"`
	Code     string          `json:"code"`
	Message  string          `json:"message"`
}

func (e BoundaryError) Error() string {
	return fmt.Sprintf("%s boundary %s: %s", e.Boundary, e.Code, e.Message)
}
