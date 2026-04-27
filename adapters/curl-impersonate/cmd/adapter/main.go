// SPDX-License-Identifier: Apache-2.0

// Command spectre-curl-impersonate is the Spectre curl-impersonate
// driver adapter.
//
// The adapter wraps the curl-impersonate binary (default
// `curl_chrome116`, override via SPECTRE_CURL_VARIANT) as a
// per-request subprocess and exposes a gRPC Driver server on a TCP
// listener. PR11 implemented Initialize + Navigate; PR12 closed the
// v1alpha1 unary surface (Close, Query, Extract). R2.2 swapped the
// Unix-domain-socket transport for TCP and registered the gRPC
// standard health check (ADR-0021, ADR-0022); the wire-level
// service definitions in proto/spectre/driver/v1alpha1 are
// unchanged.
//
// Lifecycle:
//
//  1. Resolve the bind port from SPECTRE_ADAPTER_GRPC_PORT (ADR-0021
//     §4 reserves 9093 as the canonical default; the conformance
//     harness allocates a free port at test time).
//  2. Resolve the curl variant from SPECTRE_CURL_VARIANT (default
//     curl_chrome116). ADR-0016 §3.
//  3. Sweep stale cookie-jar files left by crashed prior runs.
//     ADR-0016 §4.
//  4. Bind a gRPC server to 0.0.0.0:<port>, register the Driver
//     service and the gRPC standard health check (SERVING from
//     startup), and serve.
//  5. Wait for SIGTERM/SIGINT. On signal: stop the gRPC server
//     gracefully (drains in-flight RPCs up to a deadline), close
//     every session (removing its cookie-jar file), and exit zero.
package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/curlx"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/server"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/sessions"
	driverv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/driver/v1alpha1"
)

const (
	binaryName = "spectre-curl-impersonate"
	version    = "0.1.0-alpha.0"

	// shutdownDeadline matches the Playwright/SeleniumBase
	// adapters: five seconds for graceful drain before the harness
	// escalates.
	shutdownDeadline = 5 * time.Second

	// portEnvVar names the env var the harness sets and that
	// production deployments populate via Compose / Kubernetes.
	// ADR-0021 §4.
	portEnvVar = "SPECTRE_ADAPTER_GRPC_PORT"
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

	// Stale-jar sweep before binding so a crashed prior run does
	// not leak cookie state into the new run's namespace.
	mgr := sessions.NewManager()
	if err := mgr.SweepStale(); err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: failed to sweep stale cookie jars: %v\n", err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return fmt.Errorf("listen on 0.0.0.0:%d: %w", port, err)
	}

	grpcServer := grpc.NewServer()
	driverv1alpha1.RegisterDriverServer(grpcServer, server.New(mgr, curlx.Fetch, variant))

	// ADR-0021 §6: register the gRPC standard health check. The
	// overall service status ("") is set to SERVING from process
	// startup; the conformance harness polls Check until it
	// returns SERVING, and production health probes consume the
	// same endpoint.
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
		// Server.Serve returns nil on graceful stop; a non-nil
		// error here means an unexpected listener failure.
		if err != nil {
			gracefulCleanup(grpcServer, mgr)
			return fmt.Errorf("grpc serve: %w", err)
		}
	}

	gracefulCleanup(grpcServer, mgr)
	return nil
}

func gracefulCleanup(srv *grpc.Server, mgr *sessions.Manager) {
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
}

// resolvePort reads the bind port from SPECTRE_ADAPTER_GRPC_PORT.
// The env var is required and must parse as an integer in the
// valid TCP port range. The conformance harness allocates a free
// port and injects it; production deployments use the canonical
// 9093 reserved by ADR-0021 §4.
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
