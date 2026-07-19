package authfile

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/flock"
)

// Lock acquires a bounded cross-process lock and returns an idempotent unlock function.
func Lock(ctx context.Context, path string) (func(), error) {
	f := flock.New(path)
	locked, err := f.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if !locked {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		return nil, fmt.Errorf("lock %s: not acquired", path)
	}
	return func() { _ = f.Unlock() }, nil
}
