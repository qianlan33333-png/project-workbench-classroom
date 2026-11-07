package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeploymentDoesNotConsumeUntilCommitAndChannelsReady(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploying")
	write := func(value string) {
		t.Helper()
		if err := os.WriteFile(path+".next", []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+".next", path); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"kind":"native","committed":false}`)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- waitForDeployment(ctx, path, make(chan error)) }()
	for _, value := range []string{`{"kind":"native","committed":false,"backgroundEnabled":true}`, `{"kind":"native","committed":true,"backgroundEnabled":false}`} {
		write(value)
		select {
		case err := <-done:
			t.Fatalf("background released early: %v", err)
		case <-time.After(300 * time.Millisecond):
		}
	}
	write(`{"kind":"native","committed":true,"backgroundEnabled":true}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDeploymentCancelFailureAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploying")
	if err := waitForDeployment(context.Background(), path, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"kind":"native","restoring":true,"backgroundEnabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := waitForDeployment(context.Background(), path, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"kind":"native"}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForDeployment(ctx, path, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	failure := make(chan error, 1)
	failure <- errors.New("listener stopped")
	if err := waitForDeployment(context.Background(), path, failure); err == nil || err.Error() != "listener stopped" {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`not json`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := waitForDeployment(context.Background(), path, nil); err == nil {
		t.Fatal("corrupt gate opened")
	}
}
