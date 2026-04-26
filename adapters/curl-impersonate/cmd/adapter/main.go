// SPDX-License-Identifier: Apache-2.0

// Command spectre-curl-impersonate is the Spectre curl-impersonate
// driver adapter.
//
// The adapter wraps the curl-impersonate binary (default
// `curl_chrome116`, override via SPECTRE_CURL_VARIANT) as a
// per-request subprocess and exposes a gRPC Driver server over a
// Unix domain socket. PR11 implements Initialize + Navigate; the
// other RPCs return codes.Unimplemented and arrive in PR12.
//
// Lifecycle (mirrors the Playwright and SeleniumBase adapters
// from ADR-0008 §2):
//
//  1. Resolve the socket path from `--socket=<path>` (CLI flag
//     wins) or the SPECTRE_DRIVER_SOCKET env var.
//  2. Resolve the curl variant from SPECTRE_CURL_VARIANT (default
//     curl_chrome116). ADR-0016 §3.
//  3. Sweep stale cookie-jar files left by crashed prior runs.
//     ADR-0016 §4.
//  4. Bind a gRPC server to the socket, register the Driver
//     service, and write `ready unix:<path>\n` to stdout once the
//     listener is accepting connections.
//  5. Wait for SIGTERM/SIGINT. On signal: stop the gRPC server
//     gracefully (drains in-flight RPCs up to a deadline), close
//     every session (removing its cookie-jar file), unlink the
//     socket, and exit zero.
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/curlx"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/server"
	"github.com/FabioCaffarello/spectre/adapters/curl-impersonate/internal/sessions"
	driverv1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/driver/v1alpha1"
)

const (
	binaryName = "spectre-curl-impersonate"
	version    = "0.1.0-alpha.0"

	// shutdownDeadline matches the Playwright/SeleniumBase
	// adapters (ADR-0008 §2): five seconds for graceful drain
	// before the harness escalates.
	shutdownDeadline = 5 * time.Second
)

// protocolVersion is sourced from the generated protobuf package
// path so the engine, the control plane, and every adapter share
// one provenance. See ADR-0007.
var protocolVersion = string(driverv1alpha1.File_spectre_driver_v1alpha1_driver_proto.Package())

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run is the testable entry point: argv slice in, stdout/stderr
// writers out, error returned. main wraps it.
func run(argv []string, stdout, stderr *os.File) error {
	socketPath, err := resolveSocketPath(argv)
	if err != nil {
		return err
	}
	variant := resolveVariant()

	// Stale-jar sweep before binding so a crashed prior run does
	// not leak cookie state into the new run's namespace.
	mgr := sessions.NewManager()
	if err := mgr.SweepStale(); err != nil {
		fmt.Fprintf(stderr, "warning: failed to sweep stale cookie jars: %v\n", err)
	}

	// Replace any prior socket file. The adapter is the sole
	// owner of its socket path (ADR-0008 §2).
	_ = os.Remove(socketPath)
	if err := ensureParent(socketPath); err != nil {
		return err
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}

	grpcServer := grpc.NewServer()
	driverv1alpha1.RegisterDriverServer(grpcServer, server.New(mgr, curlx.Fetch, variant))

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- grpcServer.Serve(listener)
	}()

	fmt.Fprintf(stdout, "ready unix:%s\n", socketPath)
	_ = stdout.Sync()
	fmt.Fprintf(stderr, "%s %s (driver protocol %s) variant=%s listening on unix:%s\n",
		binaryName, version, protocolVersion, variant, socketPath)
	_ = stderr.Sync()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-signals:
		fmt.Fprintf(stderr, "received signal %v, shutting down\n", sig)
	case err := <-serveErr:
		// Server.Serve returns nil on graceful stop; a non-nil
		// error here means an unexpected listener failure.
		if err != nil {
			gracefulCleanup(grpcServer, mgr, socketPath)
			return fmt.Errorf("grpc serve: %w", err)
		}
	}

	gracefulCleanup(grpcServer, mgr, socketPath)
	return nil
}

func gracefulCleanup(srv *grpc.Server, mgr *sessions.Manager, socketPath string) {
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
	_ = os.Remove(socketPath)
}

func resolveSocketPath(argv []string) (string, error) {
	fs := flag.NewFlagSet(binaryName, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	socketFlag := fs.String("socket", "", "absolute path to the Unix domain socket the adapter binds to")
	if err := fs.Parse(argv); err != nil {
		return "", err
	}

	candidate := *socketFlag
	if candidate == "" {
		candidate = os.Getenv("SPECTRE_DRIVER_SOCKET")
	}
	if candidate == "" {
		return "", fmt.Errorf("no socket path provided: pass --socket=<absolute-path> or set SPECTRE_DRIVER_SOCKET")
	}
	if !filepath.IsAbs(candidate) {
		return "", fmt.Errorf("socket path must be absolute, got %q", candidate)
	}
	return candidate, nil
}

func resolveVariant() string {
	if v := os.Getenv("SPECTRE_CURL_VARIANT"); v != "" {
		return v
	}
	return curlx.DefaultVariant
}

func ensureParent(path string) error {
	parent := filepath.Dir(path)
	if parent == "" || parent == "." {
		return nil
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", parent, err)
	}
	return nil
}
