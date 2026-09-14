package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestMergedFramingKeepsHeaderAndBodyBudgets(t *testing.T) {
	for _, extra := range []int{0, 1} {
		header := "X-Header: " + strings.Repeat("x", maxLSPHeaderLineBytes-len("X-Header: ")-1+extra) + "\n"
		srv := NewServer(strings.NewReader(header+"Content-Length: 2\r\n\r\n{}"), &bytes.Buffer{}, "test")
		payload, err := srv.readFramedMessage()
		if extra == 0 && (err != nil || string(payload) != "{}") {
			t.Fatalf("header exactly at byte bound rejected: %v", err)
		}
		if extra == 1 && !errors.Is(err, errLineTooLong) {
			t.Fatalf("oversize header accepted: %v", err)
		}
	}
	inline := "{" + strings.Repeat(" ", maxLSPMessageSize-2) + "}"
	for _, framed := range []bool{false, true} {
		input := inline + "\n"
		if framed {
			input = fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(inline), inline)
		}
		srv := NewServer(strings.NewReader(input), &bytes.Buffer{}, "test")
		payload, err := srv.readFramedMessage()
		if err != nil || len(payload) != maxLSPMessageSize {
			t.Fatalf("4 MiB payload rejected (framed=%t): %d, %v", framed, len(payload), err)
		}
	}
}
func TestMergedSessionBudgetStopsBothProducerAndConsumer(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload)
	var out bytes.Buffer
	srv := NewServer(strings.NewReader(strings.Repeat(frame, 3)), &out, "test")
	if err := srv.runSession(context.Background(), 2); !errors.Is(err, errTooManyLSPMessages) {
		t.Fatalf("finite session budget must fail closed: %v", err)
	}
	if got := strings.Count(out.String(), "Content-Length:"); got != 2 {
		t.Fatalf("processed %d requests, want 2", got)
	}
	exit := `{"jsonrpc":"2.0","method":"exit"}`
	srv = NewServer(strings.NewReader(frame+fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(exit), exit)), &bytes.Buffer{}, "test")
	if err := srv.runSession(context.Background(), 2); err != nil {
		t.Fatalf("explicit exit at budget boundary must succeed: %v", err)
	}
}
