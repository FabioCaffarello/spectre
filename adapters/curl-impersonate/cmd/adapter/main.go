// SPDX-License-Identifier: Apache-2.0

// Command spectre-curl-impersonate is the Spectre curl-impersonate
// driver adapter.
//
// The adapter wraps the curl-impersonate binary (default
// `curl_chrome116`, override via SPECTRE_CURL_VARIANT) as a
// per-request subprocess and exposes a gRPC Driver server on a TCP
// listener. R4.3 externalises session metadata to Redis (ADR-0023
// §4 + §5): the adapter PINGs Redis at startup and exits non-zero
// on failure (§6 — Redis required), then participates in the §5
// restart-invalidation contract via a per-process UUID
// (overridable via SPECTRE_ADAPTER_INSTANCE_ID for the conformance
// suite).
//
// Lifecycle:
//
//  1. Resolve SPECTRE_ADAPTER_GRPC_PORT (ADR-0021 §4),
//     SPECTRE_REDIS_URL (ADR-0023 §4), SPECTRE_CURL_VARIANT
//     (ADR-0016 §3), and the optional
//     SPECTRE_ADAPTER_INSTANCE_ID override (ADR-0023 §5 R4.3).
//  2. Construct a redis client and PING it. Exit non-zero if the
//     ping fails — the Compose `depends_on.condition:
//     service_healthy` and equivalent Helm gates rely on this.
//  3. Sweep stale cookie-jar files from prior runs (ADR-0016 §4).
//  4. Bind a gRPC server to 0.0.0.0:<port>, register the Driver
//     service and the gRPC standard health check (SERVING from
//     startup), and serve.
//  5. Wait for SIGTERM/SIGINT. On signal: stop the gRPC server
//     gracefully (drains in-flight RPCs up to a deadline), close
//     every session (removing its cookie-jar file), disconnect
//     from Redis, and exit zero.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/curlx"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/logging"
	redisx "github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/redis"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/server"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/sessions"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/telemetry"
	adaptertls "github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/tls"
	driverv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/driver/v1alpha1"
)

const (
	binaryName = "spectre-curl-impersonate"
	version    = "0.1.0-alpha.2"

	// shutdownDeadline matches the Playwright/SeleniumBase
	// adapters: five seconds for graceful drain before the harness
	// escalates.
	shutdownDeadline = 5 * time.Second

	// portEnvVar names the env var the harness sets and that
	// production deployments populate via Compose / Kubernetes.
	// ADR-0021 §4.
	portEnvVar = "SPECTRE_ADAPTER_GRPC_PORT"

	// redisURLEnvVar names the Redis URL env var. ADR-0023 §4.
	redisURLEnvVar = "SPECTRE_REDIS_URL"

	// instanceIDEnvVar — test-only override for the per-process
	// UUID the §5 restart-invalidation mechanism keys on. Production
	// deployments leave this unset.
	instanceIDEnvVar = "SPECTRE_ADAPTER_INSTANCE_ID"

	// defaultRedisURL — local-dev fallback. Production must set
	// the env var explicitly so a misconfiguration surfaces at
	// deploy time.
	defaultRedisURL = "redis://127.0.0.1:6379/0"

	// redisPingTimeout caps the startup PING. Five seconds covers
	// a healthy local Compose stack with margin; deployments where
	// Redis takes longer than this to come up are misconfigured.
	redisPingTimeout = 5 * time.Second

	// metricsPortEnvVar names the env var the chart + Compose
	// populate to set the Prometheus `/metrics` sidecar bind port.
	// Uniform 9090 across the catalog per ADR-0031 §3.3.
	metricsPortEnvVar = "SPECTRE_METRICS_PORT"

	// defaultMetricsPort is the ADR-0031 §3.3 uniform port. Honoured
	// when `SPECTRE_METRICS_PORT` is unset (local-dev convenience
	// — production deployments inject the env var from chart values).
	defaultMetricsPort = 9090
)

// protocolVersion is sourced from the generated protobuf package
// path so the engine, the control plane, and every adapter share
// one provenance. See ADR-0007.
var protocolVersion = string(driverv1alpha1.File_spectre_driver_v1alpha1_driver_proto.Package())

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run is the testable entry point. main wraps it.
func run() error {
	// W3.2 Cluster C: install the JSON stdout logger BEFORE any
	// other startup work so the redis-dial + sweep messages emit
	// in the canonical ADR-0031 §3.4 schema. slog.Default() is
	// global; setting it once at the top of run lets every callee
	// reach the same handler via `slog.InfoContext(ctx, ...)`.
	logger := logging.New(os.Stdout, telemetry.ServiceName, version)
	slog.SetDefault(logger)

	port, err := resolvePort()
	if err != nil {
		return err
	}
	variant := resolveVariant()
	redisURL := resolveRedisURL()
	instanceID := resolveInstanceID()

	// W3.2 Cluster C: ADR-0031 observability foundation. The tracer
	// provider always registers a W3C propagator; the OTLP exporter
	// attaches only when `OTEL_EXPORTER_OTLP_ENDPOINT` is set
	// (same optional-exporter pattern as engine + operator). The
	// Prometheus metrics live on a separate HTTP sidecar bound to
	// `:9090` by default — ADR-0031 §3.3 uniform port.
	telemetryCtx, telemetryCancel := context.WithTimeout(context.Background(), 5*time.Second)
	telemetryShutdown, err := telemetry.Init(telemetryCtx, version)
	telemetryCancel()
	if err != nil {
		return fmt.Errorf("telemetry init: %w", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if shutdownErr := telemetryShutdown(shutdownCtx); shutdownErr != nil {
			slog.Warn("telemetry shutdown failed", "error", shutdownErr.Error())
		}
	}()

	registry := prometheus.NewRegistry()
	metrics, err := telemetry.Register(registry)
	if err != nil {
		return fmt.Errorf("metrics register: %w", err)
	}

	metricsPort, err := resolveMetricsPort()
	if err != nil {
		return err
	}
	metricsSrv := startMetricsServer(metricsPort, registry)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = metricsSrv.Shutdown(shutdownCtx)
	}()

	redis, err := redisx.FromURL(redisURL)
	if err != nil {
		return fmt.Errorf("redis client init: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(context.Background(), redisPingTimeout)
	if err := redis.Ping(pingCtx); err != nil {
		cancel()
		_ = redis.Close()
		return fmt.Errorf("redis ping at %s: %w", redisURL, err)
	}
	cancel()

	slog.Info("redis ready",
		"redis_url", redisURL,
		"adapter_instance_id", instanceID,
	)

	// Stale-jar sweep before binding so a crashed prior run does
	// not leak cookie state into the new run's namespace.
	mgr := sessions.NewManager(redis, instanceID)
	if err := mgr.SweepStale(); err != nil {
		slog.Warn("failed to sweep stale cookie jars", "error", err.Error())
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		_ = redis.Close()
		return fmt.Errorf("listen on 0.0.0.0:%d: %w", port, err)
	}

	// W3.2 Cluster C: `otelgrpc.NewServerHandler()` extracts the
	// W3C `traceparent` from incoming RPC metadata and opens a
	// server-kind span as a child of the engine's client-kind
	// span (Cluster B of W3.1). The handler also auto-emits the
	// gRPC stats it observes.
	//
	// W3.4 Cluster B: when `SPECTRE_TLS_{CERT,KEY,CA}_PATH` env
	// vars are set, the adapter requires client certificates per
	// ADR-0032 §4.2 (only the engine is an authorised caller).
	// `tlsCfg.Mode == ModePlaintext` yields nil creds, preserving
	// the v1alpha1 dial path. Partial env state is fail-fast.
	tlsCfg, err := adaptertls.DetectMode()
	if err != nil {
		return fmt.Errorf("tls: resolve mode: %w", err)
	}
	serverCreds, err := adaptertls.NewServerCredentials(tlsCfg)
	if err != nil {
		return fmt.Errorf("tls: build server credentials (mode=%s): %w", tlsCfg.Mode, err)
	}
	grpcOpts := []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	}
	if serverCreds != nil {
		grpcOpts = append(grpcOpts, grpc.Creds(serverCreds))
	}
	// Use `tls_mode` not `mode` for consistency with the Python +
	// TypeScript adapters and with the engine's W3.3 log shape;
	// `tools/test/verify-mtls-handshake.sh` greps for the
	// `tls_mode=mutual` token across all four services.
	slog.Info("tls ready", "tls_mode", tlsCfg.Mode.String())
	grpcServer := grpc.NewServer(grpcOpts...)
	driverv1alpha1.RegisterDriverServer(grpcServer, server.New(mgr, curlx.Fetch, variant, metrics))

	// ADR-0021 §6: register the gRPC standard health check.
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- grpcServer.Serve(listener)
	}()

	slog.Info("adapter listening",
		"binary", binaryName,
		"version", version,
		"protocol", protocolVersion,
		"variant", variant,
		"grpc_port", port,
		"metrics_port", metricsPort,
	)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-signals:
		slog.Info("shutting down", "signal", sig.String())
	case err := <-serveErr:
		if err != nil {
			gracefulCleanup(grpcServer, mgr, redis)
			return fmt.Errorf("grpc serve: %w", err)
		}
	}

	gracefulCleanup(grpcServer, mgr, redis)
	return nil
}

// resolveMetricsPort reads SPECTRE_METRICS_PORT (default 9090).
func resolveMetricsPort() (int, error) {
	raw := os.Getenv(metricsPortEnvVar)
	if raw == "" {
		return defaultMetricsPort, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a port number, got %q", metricsPortEnvVar, raw)
	}
	if port < 0 || port > 65535 {
		return 0, fmt.Errorf("%s must be between 0 and 65535, got %d", metricsPortEnvVar, port)
	}
	return port, nil
}

// startMetricsServer spawns an `http.Server` on `:port` serving
// `/metrics` from `registry`. The server runs in a goroutine; the
// caller's `defer` invokes `Shutdown` on the returned handle.
// ADR-0031 §3.3 mandates the `/metrics` surface — a bind failure
// is logged but not fatal (the bind error surfaces on the first
// scrape attempt instead; the adapter's primary job is gRPC
// driver requests, not metrics).
func startMetricsServer(port int, registry *prometheus.Registry) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		Registry: registry,
	}))
	srv := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("metrics server terminated unexpectedly", "error", err.Error())
		}
	}()
	slog.Info("metrics sidecar listening", "port", port)
	return srv
}

func gracefulCleanup(srv *grpc.Server, mgr *sessions.Manager, redis *redisx.Client) {
	stopped := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(shutdownDeadline):
		srv.Stop()
	}
	mgr.CloseAll()
	_ = redis.Close()
}

// resolvePort reads the bind port from SPECTRE_ADAPTER_GRPC_PORT.
// The env var is required and must parse as an integer in the
// valid TCP port range.
func resolvePort() (int, error) {
	raw := os.Getenv(portEnvVar)
	if raw == "" {
		return 0, fmt.Errorf("%s is required: set it to the TCP port the adapter should bind", portEnvVar)
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a port number, got %q", portEnvVar, raw)
	}
	if port < 0 || port > 65535 {
		return 0, fmt.Errorf("%s must be between 0 and 65535, got %d", portEnvVar, port)
	}
	return port, nil
}

func resolveVariant() string {
	if v := os.Getenv("SPECTRE_CURL_VARIANT"); v != "" {
		return v
	}
	return curlx.DefaultVariant
}

// resolveRedisURL reads SPECTRE_REDIS_URL or falls back to a
// local-dev default. ADR-0023 §4.
func resolveRedisURL() string {
	if v := os.Getenv(redisURLEnvVar); v != "" {
		return v
	}
	return defaultRedisURL
}

// resolveInstanceID returns the per-process UUID (or env-var
// override). The override is test-only — production must leave
// it unset. ADR-0023 §5 R4.3 addendum.
func resolveInstanceID() string {
	if v := os.Getenv(instanceIDEnvVar); v != "" {
		return v
	}
	return uuid.NewString()
}
