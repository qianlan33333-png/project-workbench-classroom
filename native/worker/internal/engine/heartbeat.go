package engine

import (
	"context"
	"go.temporal.io/sdk/activity"
	"time"
)

func keepAlive(ctx context.Context) func() {
	done := make(chan struct{})
	activity.RecordHeartbeat(ctx)
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				activity.RecordHeartbeat(ctx)
			}
		}
	}()
	return func() { close(done) }
}
