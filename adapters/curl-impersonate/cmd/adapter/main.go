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
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/curlx"
	redisx "github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/redis"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/server"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/sessions"
	driverv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/driver/v1alpha1"
)

const (
	binaryName = "spectre-curl-impersonate"
	version    = "0.1.0-alpha.1"

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
)

// protocolVersion is sourced from the generated protobuf package
// path so the engine, the control plane, and every adapter share
// one provenance. See ADR-0007.
var protocolVersion = string(driverv1alpha1.File_spectre_driver_v1alpha1_driver_proto.Package())

func main() {
	if err := run(os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run is the testable entry point: stderr writer in, error
// returned. main wraps it.
func run(stderr *os.File) error {
	port, err := resolvePort()
	if err != nil {
		return err
	}
	variant := resolveVariant()
	redisURL := resolveRedisURL()
	instanceID := resolveInstanceID()

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

	_, _ = fmt.Fprintf(stderr, "redis ready at %s (adapter_instance_id=%s)\n", redisURL, instanceID)
	_ = stderr.Sync()

	// Stale-jar sweep before binding so a crashed prior run does
	// not leak cookie state into the new run's namespace.
	mgr := sessions.NewManager(redis, instanceID)
	if err := mgr.SweepStale(); err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: failed to sweep stale cookie jars: %v\n", err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		_ = redis.Close()
		return fmt.Errorf("listen on 0.0.0.0:%d: %w", port, err)
	}

	grpcServer := grpc.NewServer()
	driverv1alpha1.RegisterDriverServer(grpcServer, server.New(mgr, curlx.Fetch, variant))

	// ADR-0021 §6: register the gRPC standard health check.
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- grpcServer.Serve(listener)
	}()

	_, _ = fmt.Fprintf(stderr, "%s %s (driver protocol %s) variant=%s listening on 0.0.0.0:%d\n",
		binaryName, version, protocolVersion, variant, port)
	_ = stderr.Sync()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-signals:
		_, _ = fmt.Fprintf(stderr, "received signal %v, shutting down\n", sig)
	case err := <-serveErr:
		if err != nil {
			gracefulCleanup(grpcServer, mgr, redis)
			return fmt.Errorf("grpc serve: %w", err)
		}
	}

	gracefulCleanup(grpcServer, mgr, redis)
	return nil
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
