/*
Copyright 2026 Fabio Caffarello.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	enginev1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/engine/v1alpha1"
)

const bufSize = 1024 * 1024

// mockEngineServer implements enginev1alpha1.EngineServer with a
// configurable event sequence. Tests assemble a script of events
// (Row / Completed / Failed / hang) and the server emits them in
// order over the streaming response.
type mockEngineServer struct {
	enginev1alpha1.UnimplementedEngineServer
	events []func(stream grpc.ServerStreamingServer[enginev1alpha1.RunJobResponse]) error
}

func (m *mockEngineServer) RunJob(_ *enginev1alpha1.RunJobRequest, stream grpc.ServerStreamingServer[enginev1alpha1.RunJobResponse]) error {
	for _, ev := range m.events {
		if err := ev(stream); err != nil {
			return err
		}
	}
	return nil
}

func sendRow(line string) func(grpc.ServerStreamingServer[enginev1alpha1.RunJobResponse]) error {
	return func(stream grpc.ServerStreamingServer[enginev1alpha1.RunJobResponse]) error {
		return stream.Send(&enginev1alpha1.RunJobResponse{
			Event: &enginev1alpha1.RunJobResponse_Row{Row: &enginev1alpha1.Row{JsonLine: line}},
		})
	}
}

func sendCompleted(rows int64) func(grpc.ServerStreamingServer[enginev1alpha1.RunJobResponse]) error {
	return func(stream grpc.ServerStreamingServer[enginev1alpha1.RunJobResponse]) error {
		return stream.Send(&enginev1alpha1.RunJobResponse{
			Event: &enginev1alpha1.RunJobResponse_Completed{Completed: &enginev1alpha1.Completed{RowsExtracted: rows}},
		})
	}
}

func sendFailed(code, message string) func(grpc.ServerStreamingServer[enginev1alpha1.RunJobResponse]) error {
	return func(stream grpc.ServerStreamingServer[enginev1alpha1.RunJobResponse]) error {
		return stream.Send(&enginev1alpha1.RunJobResponse{
			Event: &enginev1alpha1.RunJobResponse_Failed{Failed: &enginev1alpha1.Failed{
				ErrorCode:    code,
				ErrorMessage: message,
			}},
		})
	}
}

// hangUntilCancelled blocks the server-side handler until the
// stream's context is cancelled. Used to simulate a slow / unresponsive
// engine so the runner's cancellation path can be exercised.
func hangUntilCancelled() func(grpc.ServerStreamingServer[enginev1alpha1.RunJobResponse]) error {
	return func(stream grpc.ServerStreamingServer[enginev1alpha1.RunJobResponse]) error {
		<-stream.Context().Done()
		return stream.Context().Err()
	}
}

// startMockEngine boots a gRPC server backed by an in-process bufconn
// listener and returns a dialFunc that the EngineClientRunner can
// inject. The cleanup function stops the server on test exit.
func startMockEngine(t *testing.T, mock enginev1alpha1.EngineServer) func(ctx context.Context, endpoint string) (*grpc.ClientConn, error) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	enginev1alpha1.RegisterEngineServer(srv, mock)

	done := make(chan struct{})
	go func() {
		_ = srv.Serve(lis)
		close(done)
	}()

	t.Cleanup(func() {
		srv.Stop()
		<-done
	})

	return func(ctx context.Context, _ string) (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough://bufnet",
			grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
				return lis.Dial()
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
	}
}

func TestEngineClientRunner_StreamsRowsAndReturnsCount(t *testing.T) {
	mock := &mockEngineServer{events: []func(grpc.ServerStreamingServer[enginev1alpha1.RunJobResponse]) error{
		sendRow(`{"i":0}`),
		sendRow(`{"i":1}`),
		sendRow(`{"i":2}`),
		sendCompleted(3),
	}}
	dial := startMockEngine(t, mock)

	r := &EngineClientRunner{EngineEndpoint: "bufnet", dialFunc: dial}

	var buf bytes.Buffer
	rows, err := r.Run(context.Background(), "spectre: v1alpha1\n", &buf)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if rows != 3 {
		t.Fatalf("rows = %d, want 3", rows)
	}
	got := strings.Count(buf.String(), "\n")
	if got != 3 {
		t.Fatalf("writer received %d lines, want 3; output=%q", got, buf.String())
	}
	if !strings.Contains(buf.String(), `{"i":0}`) {
		t.Fatalf("writer missing first row; output=%q", buf.String())
	}
}

func TestEngineClientRunner_FailedEventReturnsError(t *testing.T) {
	mock := &mockEngineServer{events: []func(grpc.ServerStreamingServer[enginev1alpha1.RunJobResponse]) error{
		sendRow(`{"i":0}`),
		sendFailed("DriverError", "simulated downstream failure"),
	}}
	dial := startMockEngine(t, mock)

	r := &EngineClientRunner{EngineEndpoint: "bufnet", dialFunc: dial}

	rows, err := r.Run(context.Background(), "spectre: v1alpha1\n", &bytes.Buffer{})
	if err == nil {
		t.Fatal("Run() returned nil error, want non-nil")
	}
	// rows reflects events observed before Failed (1 row was streamed).
	if rows != 1 {
		t.Fatalf("rows = %d, want 1 (one Row before Failed)", rows)
	}
	if !strings.Contains(err.Error(), "DriverError") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "DriverError")
	}
	if !strings.Contains(err.Error(), "simulated downstream failure") {
		t.Fatalf("error = %q, want the engine's error_message", err.Error())
	}
}

func TestEngineClientRunner_HonoursContextCancellation(t *testing.T) {
	mock := &mockEngineServer{events: []func(grpc.ServerStreamingServer[enginev1alpha1.RunJobResponse]) error{
		sendRow(`{"i":0}`),
		hangUntilCancelled(),
	}}
	dial := startMockEngine(t, mock)

	r := &EngineClientRunner{EngineEndpoint: "bufnet", dialFunc: dial}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	rows, err := r.Run(ctx, "spectre: v1alpha1\n", &bytes.Buffer{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Run() returned nil error, want context error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want DeadlineExceeded or Canceled", err)
	}
	// Streamed at least the first row before the hang.
	if rows != 1 {
		t.Fatalf("rows = %d, want 1 (one row observed before hang)", rows)
	}
	if elapsed > time.Second {
		t.Fatalf("Run() took %v; want sub-second cancellation", elapsed)
	}
}

func TestEngineClientRunner_RejectsEmptyEndpoint(t *testing.T) {
	r := &EngineClientRunner{}
	_, err := r.Run(context.Background(), "spectre: v1alpha1\n", &bytes.Buffer{})
	if err == nil {
		t.Fatal("Run() with empty EngineEndpoint returned nil, want non-nil error")
	}
	if !strings.Contains(err.Error(), "endpoint is empty") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "endpoint is empty")
	}
}

func TestEngineClientRunner_DialFailureSurfacesError(t *testing.T) {
	// Inject a dialer that always fails so the runner's dial-error
	// branch is exercised deterministically without depending on
	// kernel TCP semantics for an unreachable endpoint.
	failingDial := func(_ context.Context, _ string) (*grpc.ClientConn, error) {
		return nil, fmt.Errorf("simulated dial failure")
	}

	r := &EngineClientRunner{EngineEndpoint: "127.0.0.1:9090", dialFunc: failingDial}

	rows, err := r.Run(context.Background(), "spectre: v1alpha1\n", &bytes.Buffer{})
	if err == nil {
		t.Fatal("Run() returned nil error, want dial error")
	}
	if rows != 0 {
		t.Fatalf("rows = %d, want 0 on dial failure", rows)
	}
	if !strings.Contains(err.Error(), "dial 127.0.0.1:9090") {
		t.Fatalf("error = %q, want endpoint in error message", err.Error())
	}
	if !strings.Contains(err.Error(), "simulated dial failure") {
		t.Fatalf("error = %q, want underlying dial error preserved", err.Error())
	}
}

// engineClientFailingWriter returns the configured error on every Write,
// surfacing the writer-error branch of Run.
type engineClientFailingWriter struct{ err error }

func (w *engineClientFailingWriter) Write(_ []byte) (int, error) { return 0, w.err }

func TestEngineClientRunner_WriterErrorSurfacesAsRunError(t *testing.T) {
	mock := &mockEngineServer{events: []func(grpc.ServerStreamingServer[enginev1alpha1.RunJobResponse]) error{
		sendRow(`{"i":0}`),
		sendCompleted(1),
	}}
	dial := startMockEngine(t, mock)

	r := &EngineClientRunner{EngineEndpoint: "bufnet", dialFunc: dial}

	_, err := r.Run(context.Background(), "spectre: v1alpha1\n",
		&engineClientFailingWriter{err: io.ErrShortWrite})
	if err == nil {
		t.Fatal("Run() returned nil, want writer error")
	}
	if !strings.Contains(err.Error(), "write row") {
		t.Fatalf("err = %q, want substring \"write row\"", err.Error())
	}
}
