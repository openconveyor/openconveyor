/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package trigger

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/go-logr/logr"
)

// Server is a manager.Runnable that binds an HTTP listener and serves
// webhook traffic through Handler. controller-runtime's manager calls
// Start(ctx); we shut the listener down when ctx is cancelled so the
// server lifecycle tracks the manager's.
type Server struct {
	Addr    string
	Handler *Handler
	Log     logr.Logger
}

// NeedLeaderElection tells controller-runtime to run the trigger server
// on every replica, not just the leader. Webhook traffic balanced by a
// Service should reach whichever replica answered DNS; the Tasks it
// creates are the reconciler's concern (which is leader-gated).
func (s *Server) NeedLeaderElection() bool { return false }

// Start serves requests until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	if s.Handler == nil {
		return errors.New("trigger server: Handler is nil")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/", s.Handler)

	srv := &http.Server{
		Addr:              s.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("trigger server listen %s: %w", s.Addr, err)
	}
	s.Log.Info("trigger webhook server listening", "addr", ln.Addr().String())

	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-serveErr:
		return err
	}
}
