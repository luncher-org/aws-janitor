package action

import (
	"context"
	"fmt"
	"time"
)

func waitUntil(ctx context.Context, timeout, interval time.Duration, check func(context.Context) (bool, error)) error {
	waitFor := time.Now().Add(timeout)
	for {
		if time.Now().After(waitFor) {
			return fmt.Errorf("timeout exceeded")
		}
		done, err := check(ctx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
