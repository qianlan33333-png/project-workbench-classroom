package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"
)

// API readiness lets the Host start without consuming business tasks. Only
// the root-owned release controller can open polling after commit and channels.
func waitForDeployment(ctx context.Context, path string, serverErrors <-chan error) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		var state struct {
			Kind              string `json:"kind"`
			Committed         bool   `json:"committed"`
			Restoring         bool   `json:"restoring"`
			BackgroundEnabled bool   `json:"backgroundEnabled"`
		}
		if err = json.Unmarshal(data, &state); err != nil {
			return err
		}
		if state.Kind == "native" && (state.Committed || state.Restoring) && state.BackgroundEnabled {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err = <-serverErrors:
			return err
		case <-ticker.C:
		}
	}
}
