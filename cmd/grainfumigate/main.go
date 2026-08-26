package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"grain-fumigation-interlock/internal/app"
	"grain-fumigation-interlock/internal/httpapi"
	"grain-fumigation-interlock/internal/replay"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Printf("error: %v", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("grainfumigate", flag.ContinueOnError)
	addr := fs.String("addr", "", "HTTP listen address")
	dataDir := fs.String("data", "", "persistent data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg := app.LoadConfig()
	if *addr != "" {
		cfg.Address = *addr
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	ctx := context.Background()
	rt, err := app.Bootstrap(ctx, cfg)
	if err != nil {
		return err
	}
	switch fs.Arg(0) {
	case "serve", "":
		return serve(ctx, rt)
	case "replay":
		manifest, err := replay.RunDeterministic(ctx, rt.Service, cfg.Anchor)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "ok", "archive": manifest})
	case "recover":
		return json.NewEncoder(os.Stdout).Encode(rt.Report)
	default:
		return fmt.Errorf("unknown command %q", fs.Arg(0))
	}
}

func serve(ctx context.Context, rt *app.Runtime) error {
	srv := &http.Server{Addr: rt.Config.Address, Handler: httpapi.New(rt.Service), ReadHeaderTimeout: 5 * time.Second}
	errs := make(chan error, 1)
	go func() {
		log.Printf("grain fumigation interlock listening on %s data=%s recovered=%d pending=%d", rt.Config.Address, rt.Config.DataDir, rt.Report.Operations, rt.Report.PendingCommands)
		errs <- srv.ListenAndServe()
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-ctx.Done():
	case <-stop:
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdownCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
