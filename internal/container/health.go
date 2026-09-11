package container

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

// HealthServer provides cloud-native HTTP probes for Kubernetes and container runtimes.
type HealthServer struct {
	server  *http.Server
	ready   atomic.Bool
	started atomic.Bool
}

// NewHealthServer initializes a HealthServer on the specified address.
func NewHealthServer(addr string) *HealthServer {
	if addr == "" {
		addr = ":8080"
	}
	hs := &HealthServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", hs.handleHealthz)
	mux.HandleFunc("/livez", hs.handleLivez)
	mux.HandleFunc("/readyz", hs.handleReadyz)

	hs.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	hs.started.Store(true)
	hs.ready.Store(true)
	return hs
}

func (hs *HealthServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK\n")); err != nil {
		return
	}
}

func (hs *HealthServer) handleLivez(w http.ResponseWriter, r *http.Request) {
	if !hs.started.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := w.Write([]byte("NOT_STARTED\n")); err != nil {
			return
		}
		return
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("LIVE\n")); err != nil {
		return
	}
}

func (hs *HealthServer) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !hs.ready.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := w.Write([]byte("NOT_READY\n")); err != nil {
			return
		}
		return
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("READY\n")); err != nil {
		return
	}
}

// SetReady toggles the readiness probe state.
func (hs *HealthServer) SetReady(ready bool) {
	if hs == nil {
		return
	}
	hs.ready.Store(ready)
}

// Start launches the HTTP server asynchronously.
func (hs *HealthServer) Start() error {
	if hs == nil || hs.server == nil {
		return fmt.Errorf("server not initialized")
	}
	go func() {
		if err := hs.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return
		}
	}()
	return nil
}

// Shutdown gracefully drains the server connections.
func (hs *HealthServer) Shutdown(ctx context.Context) error {
	if hs == nil || hs.server == nil {
		return nil
	}
	hs.ready.Store(false)
	return hs.server.Shutdown(ctx)
}

// WaitForGracefulDrain blocks until a POSIX termination signal is received.
func WaitForGracefulDrain(ctx context.Context, hs *HealthServer, drainDuration time.Duration) error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigChan:
	case <-ctx.Done():
		return ctx.Err()
	}

	drainCtx, cancel := context.WithTimeout(context.Background(), drainDuration)
	defer cancel()

	if hs != nil {
		return hs.Shutdown(drainCtx)
	}
	return nil
}
