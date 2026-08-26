package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"grain-fumigation-interlock/internal/app"
)

func TestModel_ServeHonorsShutdownDeadline(t *testing.T) {
	tests := []struct {
		name       string
		partial    bool
		finish     bool
		wantErr    bool
		maxElapsed time.Duration
	}{
		{name: "idle server exits gracefully", maxElapsed: time.Second},
		{name: "in-flight request drains gracefully", partial: true, finish: true, maxElapsed: time.Second},
		{name: "partial request is closed at deadline", partial: true, wantErr: true, maxElapsed: 5500 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := probe.Addr().String()
			if err := probe.Close(); err != nil {
				t.Fatal(err)
			}

			cfg := app.LoadConfig()
			cfg.Address = addr
			cfg.DataDir = t.TempDir()
			rt, err := app.Bootstrap(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			serveErr := make(chan error, 1)
			go func() { serveErr <- serve(ctx, rt) }()

			var conn net.Conn
			dialDeadline := time.Now().Add(2 * time.Second)
			for {
				conn, err = net.DialTimeout("tcp", addr, 50*time.Millisecond)
				if err == nil {
					break
				}
				if time.Now().After(dialDeadline) {
					cancel()
					t.Fatalf("server did not start listening: %v", err)
				}
				time.Sleep(10 * time.Millisecond)
			}

			if tt.partial {
				contentLength := 100
				if tt.finish {
					contentLength = 2
				}
				request := "POST /v1/operations HTTP/1.1\r\n" +
					"Host: " + addr + "\r\n" +
					"Content-Type: application/json\r\n" +
					fmt.Sprintf("Content-Length: %d\r\n\r\n{", contentLength)
				if _, err := fmt.Fprint(conn, request); err != nil {
					cancel()
					conn.Close()
					t.Fatal(err)
				}
				time.Sleep(100 * time.Millisecond)
			} else if err := conn.Close(); err != nil {
				cancel()
				t.Fatal(err)
			}

			started := time.Now()
			cancel()
			if tt.finish {
				time.Sleep(25 * time.Millisecond)
				if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				if _, err := fmt.Fprint(conn, "}"); err != nil {
					t.Fatalf("server did not drain request body: %v", err)
				}
				var response [1]byte
				if _, err := conn.Read(response[:]); err != nil {
					t.Fatalf("server did not finish in-flight request: %v", err)
				}
				_ = conn.Close()
			}
			var gotErr error
			select {
			case gotErr = <-serveErr:
			case <-time.After(tt.maxElapsed):
				if conn != nil {
					_ = conn.Close()
				}
				gotErr = <-serveErr
				t.Fatalf("serve exceeded shutdown limit of %v (eventual error: %v)", tt.maxElapsed, gotErr)
			}
			if elapsed := time.Since(started); elapsed > tt.maxElapsed {
				t.Fatalf("serve took %v to shut down, limit is %v", elapsed, tt.maxElapsed)
			}
			if tt.wantErr {
				if !errors.Is(gotErr, context.DeadlineExceeded) {
					t.Fatalf("serve error = %v, want shutdown deadline error", gotErr)
				}
				if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				var one [1]byte
				if _, err := conn.Read(one[:]); err == nil {
					t.Fatal("partial request connection remained readable after shutdown")
				} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
					t.Fatal("partial request connection was not released after shutdown")
				}
				_ = conn.Close()
			} else if gotErr != nil {
				t.Fatalf("idle graceful shutdown returned %v", gotErr)
			}

			rebound, err := net.Listen("tcp", addr)
			if err != nil {
				t.Fatalf("listener was not released: %v", err)
			}
			_ = rebound.Close()
		})
	}
}
