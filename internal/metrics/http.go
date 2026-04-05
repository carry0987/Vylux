package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HTTPHandler returns a standard net/http handler for Prometheus scraping.
func HTTPHandler() http.Handler {
	ensureRegistered()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		syncQueueDepth()
		promHandler := promhttp.Handler()
		promHandler.ServeHTTP(w, r)
	})
}

// NewMux creates a lightweight HTTP mux for metrics-only processes such as worker mode.
func NewMux() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", HTTPHandler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	return mux
}

// Server exposes a small HTTP listener for metrics scraping in worker-only mode.
type Server struct {
	httpServer *http.Server
}

// NewServer creates a metrics-only HTTP server on the given port.
func NewServer(port int) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:              fmt.Sprintf(":%d", port),
			Handler:           NewMux(),
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

// Start runs the metrics server until the context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		err := s.httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	slog.Info("metrics server listening", "addr", s.httpServer.Addr)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("shutdown metrics server: %w", err)
		}
		return nil
	case err := <-errCh:
		return err
	}
}
