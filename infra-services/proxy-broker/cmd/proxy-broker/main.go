// SPDX-License-Identifier: Apache-2.0

// Command proxy-broker is the Spectre proxy-broker
// infra-service entry point — slot 1 of ADR-0036 §3.1.
//
// Lifecycle:
//
//  1. Load env config via `internal/config` (fail-fast on
//     invalid TLS / provider combos).
//  2. Initialise structured slog JSON logger (ADR-0031 §3.4
//     mandated fields).
//  3. Dial Redis; ping to confirm reachability.
//  4. Construct providers from config (stub + BrightData
//     when their credentials present).
//  5. Initialise OpenTelemetry (OTLP traces when
//     `OTEL_EXPORTER_OTLP_ENDPOINT` set; Prometheus
//     `/metrics` sidecar on `SPECTRE_METRICS_PORT` always).
//  6. Construct the gRPC server (Server stats handler from
//     `otelgrpc`, TLS credentials when mTLS posture, plaintext
//     otherwise).
//  7. Register Proxy service + grpc.health.v1.Health +
//     reflection.
//  8. Serve until SIGTERM/SIGINT; graceful stop on signal.
//
// W3.3 TLS pattern + W3.1 OTel pattern + curl-impersonate
// adapter slog pattern apply here verbatim; rather than
// duplicating per-package wrappers, the wiring is inlined
// for a single-file entry point.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	proxyv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/proxy/v1alpha1"

	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/config"
	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/providers"
	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/providers/brightdata"
	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/providers/stub"
	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/server"
	"github.com/FabioCaffarello/spectre/infra-services/proxy-broker/internal/state"
)

const (
	serviceName    = "spectre-proxy-broker"
	serviceVersion = "0.1.0-alpha.3"

	shutdownGrace = 10 * time.Second
)

func main() {
	logger := newLogger()
	slog.SetDefault(logger)

	if err := run(context.Background(), logger); err != nil {
		logger.Error("proxy-broker exited with error", "error", err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logger.Info("config loaded",
		"listen_addr", cfg.ListenAddr,
		"metrics_port", cfg.MetricsPort,
		"tls_mode", cfg.TLSMode.String(),
		"stub_enabled", cfg.StubEnabled,
		"brightdata_enabled", cfg.BrightDataEnabled,
		"otlp_endpoint_set", cfg.OTLPEndpoint != "")

	// Telemetry — tracer provider + metrics sidecar.
	shutdownTracer, err := initTracer(ctx, cfg.OTLPEndpoint)
	if err != nil {
		return fmt.Errorf("telemetry: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = shutdownTracer(shutdownCtx)
	}()
	metricsRegistry := prometheus.NewRegistry()
	metricsRegistry.MustRegister(
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)
	go startMetricsSidecar(cfg.MetricsPort, metricsRegistry, logger)

	// Redis.
	rdb, err := dialRedis(ctx, cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	stateClient := state.New(rdb)
	defer func() { _ = stateClient.Close() }()
	logger.Info("redis ready", "url_redacted", redactRedisURL(cfg.RedisURL))

	// Provider registry.
	provMap, provOrder, err := buildProviders(cfg)
	if err != nil {
		return fmt.Errorf("providers: %w", err)
	}
	logger.Info("providers ready",
		"providers", provOrder,
		"preferred", provOrder[0])

	srv, err := server.New(provMap, provOrder, stateClient, logger)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}

	// gRPC server with stats handler + TLS (when mutual).
	grpcOpts := []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	}
	if cfg.TLSMode == config.TLSModeMutual {
		creds, err := loadServerTLS(cfg)
		if err != nil {
			return fmt.Errorf("tls: %w", err)
		}
		grpcOpts = append(grpcOpts, grpc.Creds(creds))
		logger.Info("tls ready", "tls_mode", "mutual")
	} else {
		logger.Info("tls ready", "tls_mode", "plaintext")
	}

	grpcServer := grpc.NewServer(grpcOpts...)
	proxyv1alpha1.RegisterProxyServer(grpcServer, srv)

	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus(proxyv1alpha1.Proxy_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)

	reflection.Register(grpcServer)

	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
	}

	logger.Info("proxy-broker listening",
		"addr", cfg.ListenAddr,
		"service", serviceName,
		"service_version", serviceVersion,
		"tls_mode", cfg.TLSMode.String())

	serveErr := make(chan error, 1)
	go func() { serveErr <- grpcServer.Serve(lis) }()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-signals:
		logger.Info("shutdown signal received", "signal", sig.String())
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("grpc serve: %w", err)
		}
	}

	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
		logger.Info("graceful shutdown complete")
	case <-time.After(shutdownGrace):
		logger.Warn("graceful shutdown timed out; forcing stop", "grace", shutdownGrace.String())
		grpcServer.Stop()
	}
	return nil
}

// -- subsystems --

func newLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: false,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// Ensure ADR-0031 §3.4 mandated `service` +
			// `service_version` show on every line. Plus
			// `service_name` for parity with the Rust + Python
			// emissions where slog/log packages name the field
			// differently.
			return a
		},
	})).With(
		"service", serviceName,
		"service_version", serviceVersion,
	)
}

func initTracer(ctx context.Context, otlpEndpoint string) (func(context.Context) error, error) {
	if otlpEndpoint == "" {
		// W3.1 Cluster A pattern: empty endpoint → no-op
		// tracer; spans still get valid IDs and propagate
		// via traceparent but are not exported.
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{}, propagation.Baggage{},
		))
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(otlpEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	return tp.Shutdown, nil
}

func startMetricsSidecar(port int, reg *prometheus.Registry, logger *slog.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	logger.Info("metrics sidecar listening", "addr", addr)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("metrics sidecar exited", "error", err.Error())
	}
}

func dialRedis(ctx context.Context, url string) (*redis.Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis URL: %w", err)
	}
	rdb := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return rdb, nil
}

func buildProviders(cfg *config.Config) (map[string]providers.Provider, []string, error) {
	provMap := map[string]providers.Provider{}
	var order []string

	// BrightData first when enabled (real provider preferred
	// over stub). Stub falls in as second-or-only provider.
	if cfg.BrightDataEnabled {
		bd, err := brightdata.New(brightdata.Config{
			Username: cfg.BrightDataUsername,
			Password: cfg.BrightDataPassword,
			Zone:     cfg.BrightDataZone,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("brightdata: %w", err)
		}
		provMap[brightdata.ProviderName] = bd
		order = append(order, brightdata.ProviderName)
	}
	if cfg.StubEnabled {
		st, err := stub.New(stub.Config{URLs: cfg.StubURLs})
		if err != nil {
			return nil, nil, fmt.Errorf("stub: %w", err)
		}
		provMap[stub.ProviderName] = st
		order = append(order, stub.ProviderName)
	}
	if len(order) == 0 {
		return nil, nil, errors.New("no provider enabled (impossible — config.Load should have rejected)")
	}
	return provMap, order, nil
}

func loadServerTLS(cfg *config.Config) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(cfg.TLSCertPath, cfg.TLSKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load server cert+key: %w", err)
	}
	caPEM, err := os.ReadFile(cfg.TLSCAPath)
	if err != nil {
		return nil, fmt.Errorf("read CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("CA file %s contained no PEM certificates", cfg.TLSCAPath)
	}
	return credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}), nil
}

// redactRedisURL strips credentials from a Redis URL for log
// emissions. Keeps `redis://host:port/db` visible without
// exposing usernames / passwords.
func redactRedisURL(raw string) string {
	opts, err := redis.ParseURL(raw)
	if err != nil {
		return "<unparseable>"
	}
	if opts.Username == "" && opts.Password == "" {
		return raw
	}
	return fmt.Sprintf("redis://***@%s/%d", opts.Addr, opts.DB)
}
