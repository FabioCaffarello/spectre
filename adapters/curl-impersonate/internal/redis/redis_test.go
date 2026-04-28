// SPDX-License-Identifier: Apache-2.0

package redis

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
)

func sampleMetadata() SessionMetadata {
	return SessionMetadata{
		SessionID:         "session-1",
		Adapter:           AdapterName,
		AdapterInstanceID: "aaaa",
		CreatedAt:         "2026-04-28T00:00:00.000Z",
		LastActiveAt:      "2026-04-28T00:00:00.000Z",
		Metadata:          map[string]any{},
	}
}

func TestPing(t *testing.T) {
	rdb, mock := redismock.NewClientMock()
	defer rdb.Close()
	mock.ExpectPing().SetVal("PONG")

	c := NewClient(rdb)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestSetSession(t *testing.T) {
	rdb, mock := redismock.NewClientMock()
	defer rdb.Close()

	meta := sampleMetadata()
	body, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	mock.ExpectSet("session:"+AdapterName+":session-1", body, time.Hour).SetVal("OK")

	c := NewClient(rdb)
	if err := c.SetSession(context.Background(), "session-1", meta); err != nil {
		t.Fatalf("SetSession: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGetSessionExisting(t *testing.T) {
	rdb, mock := redismock.NewClientMock()
	defer rdb.Close()

	meta := sampleMetadata()
	body, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	mock.ExpectGet("session:" + AdapterName + ":session-1").SetVal(string(body))

	c := NewClient(rdb)
	got, err := c.GetSession(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil metadata")
	}
	if got.AdapterInstanceID != meta.AdapterInstanceID {
		t.Fatalf("unexpected metadata: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGetSessionMissingReturnsNil(t *testing.T) {
	rdb, mock := redismock.NewClientMock()
	defer rdb.Close()

	mock.ExpectGet("session:" + AdapterName + ":ghost").RedisNil()

	c := NewClient(rdb)
	got, err := c.GetSession(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil metadata for missing key, got %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGetSessionRedisErrorPropagates(t *testing.T) {
	rdb, mock := redismock.NewClientMock()
	defer rdb.Close()

	wanted := errors.New("network down")
	mock.ExpectGet("session:" + AdapterName + ":session-1").SetErr(wanted)

	c := NewClient(rdb)
	if _, err := c.GetSession(context.Background(), "session-1"); err == nil {
		t.Fatal("expected error, got nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestDeleteSession(t *testing.T) {
	rdb, mock := redismock.NewClientMock()
	defer rdb.Close()

	mock.ExpectDel("session:" + AdapterName + ":session-1").SetVal(1)

	c := NewClient(rdb)
	if err := c.DeleteSession(context.Background(), "session-1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestDeleteSessionMissingIsNoop(t *testing.T) {
	rdb, mock := redismock.NewClientMock()
	defer rdb.Close()

	// DEL on a missing key returns 0, not an error.
	mock.ExpectDel("session:" + AdapterName + ":ghost").SetVal(0)

	c := NewClient(rdb)
	if err := c.DeleteSession(context.Background(), "ghost"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFromURLInvalid(t *testing.T) {
	if _, err := FromURL("not a url"); err == nil {
		t.Fatal("expected parse error")
	}
}
