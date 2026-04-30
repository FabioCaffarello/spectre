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
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStubRunner_ReturnsZeroAndNil(t *testing.T) {
	r := &StubRunner{SleepDuration: 5 * time.Millisecond}
	rows, err := r.Run(context.Background(), uuid.New(), "spectre: v1alpha1\n", "stdout", "", nil, nil, io.Discard)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if rows != 0 {
		t.Fatalf("Run() returned rows = %d, want 0", rows)
	}
}

func TestStubRunner_HonoursContextCancellation(t *testing.T) {
	// SleepDuration is long; the context cancels well before it
	// elapses. Run should return ctx.Err() (DeadlineExceeded), not
	// (0, nil).
	r := &StubRunner{SleepDuration: 5 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	rows, err := r.Run(ctx, uuid.New(), "spectre: v1alpha1\n", "stdout", "", nil, nil, io.Discard)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() returned err = %v, want DeadlineExceeded", err)
	}
	if rows != 0 {
		t.Fatalf("Run() returned rows = %d, want 0 on cancellation", rows)
	}
	// Sanity: should return promptly, not after the full sleep.
	if elapsed > time.Second {
		t.Fatalf("Run() took %v to honour cancellation; want sub-second", elapsed)
	}
}
