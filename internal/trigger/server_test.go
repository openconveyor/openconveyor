/*
Copyright 2026.
*/

package trigger

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestServer_StartAndShutdown(t *testing.T) {
	// Pick a random free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	c := newFakeClient(t)
	srv := &Server{
		Addr:    addr,
		Handler: newHandler(c),
		Log:     logr.Discard(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	// Wait for the server to be ready. Only a 200 counts: a reachable
	// server returning non-200 must fail the test, not slip past it.
	ready := false
	var lastStatus int
	var lastErr error
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		lastErr = err
		if err == nil {
			lastStatus = resp.StatusCode
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("server did not become ready: lastStatus=%d lastErr=%v", lastStatus, lastErr)
	}

	// Cancel the context to trigger shutdown.
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within 5s")
	}
}

func TestServer_NeedLeaderElection(t *testing.T) {
	srv := &Server{}
	if srv.NeedLeaderElection() {
		t.Error("trigger server should not require leader election")
	}
}

func TestServer_NilHandler(t *testing.T) {
	srv := &Server{Addr: "127.0.0.1:0", Log: logr.Discard()}

	err := srv.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for nil Handler")
	}
}
