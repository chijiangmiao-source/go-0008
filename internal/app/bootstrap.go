package app

import (
	"context"

	"grain-fumigation-interlock/internal/store"
	"grain-fumigation-interlock/internal/ventilation"
)

type Runtime struct {
	Config  Config
	Store   *store.JSONStore
	Service *Service
	Report  store.RecoveryReport
}

func Bootstrap(ctx context.Context, cfg Config) (*Runtime, error) {
	js, err := store.OpenJSONStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	report, err := store.Recover(ctx, js)
	if err != nil {
		return nil, err
	}
	svc := NewService(js, cfg.Standards, nil, ventilation.NewSimulatedController())
	return &Runtime{Config: cfg, Store: js, Service: svc, Report: report}, nil
}
