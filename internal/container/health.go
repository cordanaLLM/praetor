package container

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Errors returned by the health server.
var (
	// ErrServerNotInitialized is returned when Start is called on a zero or nil server.
	ErrServerNotInitialized = errors.New("health server not initialized")
	// ErrAlreadyStarted is returned when Start is called on a server already listening.
	ErrAlreadyStarted = errors.New("health server already started")
	// ErrNonPositiveDrain is returned when a drain duration is zero or negative: such a
	// deadline has already expired, so no in-flight request would ever be drained.
	ErrNonPositiveDrain = errors.New("drain duration must be positive")
)

// HealthServer provides cloud-native HTTP probes for Kubernetes and container runtimes.
//
// started and ready describe the real process state: started flips to true only once the
// listener is bound and back to false on shutdown or a serve failure, so /livez cannot
// report LIVE for a process that never bound its port.
type HealthServer struct {
	server  *http.Server
	ready   atomic.Bool
	started atomic.Bool

	mu       sync.Mutex
	listener net.Listener
	serveErr atomic.Pointer[error]
}

// NewHealthServer initializes a HealthServer on the specified address. The probes report
// NOT_STARTED / NOT_READY until Start binds the listener.
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
		Addr:              addr,
		Handler:           mux,
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}
	return hs
}

func (hs *HealthServer) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeProbe(w, http.StatusOK, "OK\n")
}

func (hs *HealthServer) handleLivez(w http.ResponseWriter, _ *http.Request) {
	if !hs.started.Load() {
		writeProbe(w, http.StatusServiceUnavailable, "NOT_STARTED\n")
		return
	}
	writeProbe(w, http.StatusOK, "LIVE\n")
}

func (hs *HealthServer) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if !hs.ready.Load() {
		writeProbe(w, http.StatusServiceUnavailable, "NOT_READY\n")
		return
	}
	writeProbe(w, http.StatusOK, "READY\n")
}

func writeProbe(w http.ResponseWriter, status int, body string) {
	w.WriteHeader(status)
	// A probe client that hangs up mid-write leaves nothing to recover: the status line is
	// already on the wire and the connection is gone.
	if _, err := w.Write([]byte(body)); err != nil {
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

// Start binds the listener synchronously and serves it in the background.
//
// Binding synchronously is the point: ListenAndServe in a goroutine hides "address
// already in use" from the caller, leaving a container that reports itself live with no
// listener at all.
func (hs *HealthServer) Start() error {
	if hs == nil || hs.server == nil {
		return ErrServerNotInitialized
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if hs.listener != nil {
		return fmt.Errorf("%w on %s", ErrAlreadyStarted, hs.listener.Addr())
	}

	listener, err := net.Listen("tcp", hs.server.Addr)
	if err != nil {
		return fmt.Errorf("failed to bind health probes on %s: %w", hs.server.Addr, err)
	}
	hs.listener = listener
	hs.serveErr.Store(nil)
	hs.started.Store(true)
	hs.ready.Store(true)

	go hs.serve(listener)
	return nil
}

func (hs *HealthServer) serve(listener net.Listener) {
	err := hs.server.Serve(listener)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return
	}
	hs.started.Store(false)
	hs.ready.Store(false)
	hs.serveErr.Store(&err)
}

// ServeErr returns the error that terminated the background Serve loop, or nil while the
// server is healthy or was shut down normally.
func (hs *HealthServer) ServeErr() error {
	if hs == nil {
		return nil
	}
	if stored := hs.serveErr.Load(); stored != nil {
		return *stored
	}
	return nil
}

// Shutdown gracefully drains the server connections and clears both probe states, so a
// process that keeps running after a shutdown no longer reports itself live.
func (hs *HealthServer) Shutdown(ctx context.Context) error {
	if hs == nil || hs.server == nil {
		return nil
	}
	hs.ready.Store(false)
	hs.started.Store(false)
	err := hs.server.Shutdown(ctx)

	hs.mu.Lock()
	hs.listener = nil
	hs.mu.Unlock()

	if err != nil {
		return fmt.Errorf("health server shutdown: %w", err)
	}
	return nil
}

// WaitForGracefulDrain blocks until a POSIX termination signal is received, then drains
// the server within drainDuration.
//
// The drain context derives from ctx, so cancelling ctx aborts a hung drain; the signal
// registration is released on return instead of leaking into the process-global signal
// map for the life of the process.
func WaitForGracefulDrain(ctx context.Context, hs *HealthServer, drainDuration time.Duration) error {
	if drainDuration <= 0 {
		return fmt.Errorf("%w, got %s", ErrNonPositiveDrain, drainDuration)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	select {
	case <-sigChan:
	case <-ctx.Done():
		return ctx.Err()
	}

	drainCtx, cancel := context.WithTimeout(ctx, drainDuration)
	defer cancel()

	if hs != nil {
		return hs.Shutdown(drainCtx)
	}
	return nil
}
