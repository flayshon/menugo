package main

import (
	"context"
	"testing"
	"time"
)

func TestServeShutsDownWhenContextIsCancelled(t *testing.T) {
	app := newTestApplication(t)
	app.config.port = 0 // any free port

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- app.serve(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v; want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after the context was cancelled")
	}
}
