/*
Copyright 2025 The Scion Authors.
*/

package telemetry

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"github.com/GoogleCloudPlatform/scion/pkg/sciontool/log"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// SpanHandler is called when spans are received.
type SpanHandler func(ctx context.Context, spans []*tracepb.ResourceSpans) error

// MetricHandler is called when metrics are received.
type MetricHandler func(ctx context.Context, metrics []*metricpb.ResourceMetrics) error

// LogHandler is called when logs are received.
type LogHandler func(ctx context.Context, logs []*logspb.ResourceLogs) error

// Receiver accepts OTLP trace and metric data via gRPC and HTTP.
type Receiver struct {
	config        *Config
	grpcServer    *grpc.Server
	httpServer    *http.Server
	handler       SpanHandler
	metricHandler MetricHandler
	logHandler    LogHandler
	mu            sync.Mutex
	running       bool
	grpcPort      int
	httpPort      int
}

// GRPCPort reports the port the gRPC receiver is actually listening on, which
// differs from the configured one when that port was taken. Zero before Start.
func (r *Receiver) GRPCPort() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.grpcPort
}

// HTTPPort reports the port the HTTP receiver is actually listening on.
// Zero before Start.
func (r *Receiver) HTTPPort() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.httpPort
}

// listenLocal binds the configured receiver port, falling back to an ephemeral
// one when it is unavailable.
//
// The receiver is per-agent; the port is not. Agents sharing a network
// namespace — every agent on a host-execution runtime — compete for the same
// 4317/4318 defaults: the first to bind wins and every other agent's telemetry
// start fails outright, losing all of its data silently. An ephemeral port
// keeps those receivers working, and the port actually bound is published to
// the harness through the OTLP endpoint environment so the pair stays matched.
func listenLocal(port int, label string) (net.Listener, int, error) {
	lis, listenErr := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if listenErr != nil {
		fallback, fallbackErr := net.Listen("tcp", ":0")
		if fallbackErr != nil {
			return nil, 0, fmt.Errorf("failed to listen on %s port %d: %w", label, port, listenErr)
		}
		actual, ok := listenerPort(fallback)
		if !ok {
			_ = fallback.Close()
			return nil, 0, fmt.Errorf("failed to listen on %s port %d: %w", label, port, listenErr)
		}
		log.Info("Telemetry %s port %d unavailable (%v); listening on %d instead",
			label, port, listenErr, actual)
		return fallback, actual, nil
	}
	actual, ok := listenerPort(lis)
	if !ok {
		_ = lis.Close()
		return nil, 0, fmt.Errorf("failed to resolve the %s listener address on port %d", label, port)
	}
	return lis, actual, nil
}

// listenerPort reports the TCP port a listener bound, resolving the ":0"
// ephemeral case to the port the kernel actually assigned.
func listenerPort(lis net.Listener) (int, bool) {
	addr, ok := lis.Addr().(*net.TCPAddr)
	if !ok {
		return 0, false
	}
	return addr.Port, true
}

// NewReceiver creates a new OTLP receiver.
func NewReceiver(config *Config, handler SpanHandler, opts ...ReceiverOption) *Receiver {
	r := &Receiver{
		config:  config,
		handler: handler,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ReceiverOption configures optional receiver behavior.
type ReceiverOption func(*Receiver)

// WithMetricHandler sets the handler for received metrics.
func WithMetricHandler(h MetricHandler) ReceiverOption {
	return func(r *Receiver) {
		r.metricHandler = h
	}
}

// WithLogHandler sets the handler for received logs.
func WithLogHandler(h LogHandler) ReceiverOption {
	return func(r *Receiver) {
		r.logHandler = h
	}
}

// Start starts the OTLP gRPC and HTTP receivers.
func (r *Receiver) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("receiver already running")
	}

	// Start gRPC server
	grpcLis, grpcPort, err := listenLocal(r.config.GRPCPort, "gRPC")
	if err != nil {
		return err
	}
	r.grpcPort = grpcPort

	r.grpcServer = grpc.NewServer()
	coltracepb.RegisterTraceServiceServer(r.grpcServer, &traceServiceServer{handler: r.handler})
	colmetricpb.RegisterMetricsServiceServer(r.grpcServer, &metricsServiceServer{handler: r.metricHandler})
	collogspb.RegisterLogsServiceServer(r.grpcServer, &logsServiceServer{handler: r.logHandler})

	go func() {
		if err := r.grpcServer.Serve(grpcLis); err != nil && err != grpc.ErrServerStopped {
			_ = err // receiver may be stopping; ignore serve errors
		}
	}()

	// Start HTTP server
	httpLis, httpPort, err := listenLocal(r.config.HTTPPort, "HTTP")
	if err != nil {
		r.grpcServer.Stop()
		return err
	}
	r.httpPort = httpPort

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", r.handleHTTPTraces)
	mux.HandleFunc("/v1/metrics", r.handleHTTPMetrics)
	mux.HandleFunc("/v1/logs", r.handleHTTPLogs)

	r.httpServer = &http.Server{
		Handler: mux,
	}

	go func() {
		// Log error but don't fail - error is intentionally ignored
		// because this is a best-effort telemetry receiver.
		_ = r.httpServer.Serve(httpLis)
	}()

	r.running = true
	return nil
}

// Stop stops the OTLP receivers.
func (r *Receiver) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return nil
	}

	var errs []error

	// Stop gRPC server
	if r.grpcServer != nil {
		r.grpcServer.GracefulStop()
	}

	// Stop HTTP server
	if r.httpServer != nil {
		if err := r.httpServer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("HTTP shutdown error: %w", err))
		}
	}

	r.running = false

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// IsRunning returns true if the receiver is running.
func (r *Receiver) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// handleHTTPTraces handles OTLP HTTP trace requests.
func (r *Receiver) handleHTTPTraces(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read the request body
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	var exportReq coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &exportReq); err != nil {
		http.Error(w, "Failed to parse OTLP request", http.StatusBadRequest)
		return
	}

	// Process spans
	if r.handler != nil {
		if err := r.handler(req.Context(), exportReq.ResourceSpans); err != nil {
			http.Error(w, "Failed to process spans", http.StatusInternalServerError)
			return
		}
	}

	// Return success response
	resp := &coltracepb.ExportTraceServiceResponse{}
	respBytes, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBytes)
}

// traceServiceServer implements the OTLP gRPC trace service.
type traceServiceServer struct {
	coltracepb.UnimplementedTraceServiceServer
	handler SpanHandler
}

// Export implements the OTLP trace export RPC.
func (s *traceServiceServer) Export(ctx context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	if s.handler != nil {
		if err := s.handler(ctx, req.ResourceSpans); err != nil {
			return nil, err
		}
	}
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

// handleHTTPMetrics handles OTLP HTTP metric requests.
func (r *Receiver) handleHTTPMetrics(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	var exportReq colmetricpb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &exportReq); err != nil {
		http.Error(w, "Failed to parse OTLP request", http.StatusBadRequest)
		return
	}

	if r.metricHandler != nil {
		if err := r.metricHandler(req.Context(), exportReq.ResourceMetrics); err != nil {
			http.Error(w, "Failed to process metrics", http.StatusInternalServerError)
			return
		}
	}

	resp := &colmetricpb.ExportMetricsServiceResponse{}
	respBytes, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBytes)
}

// metricsServiceServer implements the OTLP gRPC metrics service.
type metricsServiceServer struct {
	colmetricpb.UnimplementedMetricsServiceServer
	handler MetricHandler
}

// Export implements the OTLP metric export RPC.
func (s *metricsServiceServer) Export(ctx context.Context, req *colmetricpb.ExportMetricsServiceRequest) (*colmetricpb.ExportMetricsServiceResponse, error) {
	if s.handler != nil {
		if err := s.handler(ctx, req.ResourceMetrics); err != nil {
			return nil, err
		}
	}
	return &colmetricpb.ExportMetricsServiceResponse{}, nil
}

// handleHTTPLogs handles OTLP HTTP log requests.
func (r *Receiver) handleHTTPLogs(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	var exportReq collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &exportReq); err != nil {
		http.Error(w, "Failed to parse OTLP request", http.StatusBadRequest)
		return
	}

	if r.logHandler != nil {
		if err := r.logHandler(req.Context(), exportReq.ResourceLogs); err != nil {
			http.Error(w, "Failed to process logs", http.StatusInternalServerError)
			return
		}
	}

	resp := &collogspb.ExportLogsServiceResponse{}
	respBytes, _ := proto.Marshal(resp)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBytes)
}

// logsServiceServer implements the OTLP gRPC logs service.
type logsServiceServer struct {
	collogspb.UnimplementedLogsServiceServer
	handler LogHandler
}

// Export implements the OTLP log export RPC.
func (s *logsServiceServer) Export(ctx context.Context, req *collogspb.ExportLogsServiceRequest) (*collogspb.ExportLogsServiceResponse, error) {
	if s.handler != nil {
		if err := s.handler(ctx, req.ResourceLogs); err != nil {
			return nil, err
		}
	}
	return &collogspb.ExportLogsServiceResponse{}, nil
}
