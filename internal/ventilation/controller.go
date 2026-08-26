package ventilation

import (
	"context"
	"sync"
	"time"

	"grain-fumigation-interlock/internal/domain"
)

type VentilationControllerPort interface {
	Send(ctx context.Context, intent domain.ControlIntent) (domain.ControlAck, error)
}

type SimulatedController struct {
	mu      sync.Mutex
	acks    map[string]domain.ControlAck
	FailFor map[domain.VentilationStage]string
}

func NewSimulatedController() *SimulatedController {
	return &SimulatedController{acks: map[string]domain.ControlAck{}, FailFor: map[domain.VentilationStage]string{}}
}

func (c *SimulatedController) Send(ctx context.Context, intent domain.ControlIntent) (domain.ControlAck, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.ControlAck{}, err
	}
	if ack, ok := c.acks[intent.CommandID]; ok {
		return ack, nil
	}
	ack := domain.ControlAck{
		CommandID:   intent.CommandID,
		Stage:       intent.Stage,
		Accepted:    true,
		Controller:  "local-simulated-controller",
		ReceivedAt:  time.Now().UTC(),
		Message:     "accepted",
		PhysicalRun: true,
	}
	if msg, fail := c.FailFor[intent.Stage]; fail {
		ack.Accepted = false
		ack.Message = msg
		ack.PhysicalRun = false
	}
	c.acks[intent.CommandID] = ack
	return ack, nil
}
