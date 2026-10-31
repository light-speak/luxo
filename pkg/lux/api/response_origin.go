package api

import (
	"context"
	"errors"
	"time"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

// ResponseOriginOptions bounds origin work shared by one application binding.
// It is a per-process budget, not a distributed quota or a FIFO guarantee.
// Zero MaxWaiting requires zero WaitTimeout and rejects overflow immediately.
type ResponseOriginOptions struct {
	MaxConcurrent int
	MaxWaiting    int
	WaitTimeout   time.Duration
}

var (
	errResponseOriginConfig = errors.New("api: cache origin requires positive concurrency and an explicit bounded waiting policy")
	errResponseOriginBusy   = luxerrors.New("CacheOriginBusy", 503, "Cache origin capacity exhausted")
)

type responseOrigin struct {
	slots   chan struct{}
	waiters chan struct{}
	timeout time.Duration
}

func newResponseOrigin(options ResponseOriginOptions) (*responseOrigin, error) {
	if options.MaxConcurrent <= 0 || options.MaxWaiting < 0 ||
		options.MaxWaiting == 0 && options.WaitTimeout != 0 ||
		options.MaxWaiting > 0 && options.WaitTimeout <= 0 {
		return nil, errResponseOriginConfig
	}
	return &responseOrigin{slots: make(chan struct{}, options.MaxConcurrent),
		waiters: make(chan struct{}, options.MaxWaiting), timeout: options.WaitTimeout}, nil
}

func (o *responseOrigin) run(ctx context.Context, execute func() error) error {
	if err := o.acquire(ctx); err != nil {
		return err
	}
	defer o.release()
	return execute()
}

func (o *responseOrigin) acquire(ctx context.Context) error {
	if o == nil || o.slots == nil {
		return errResponseOriginConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case o.slots <- struct{}{}:
		return nil
	default:
	}
	select {
	case o.waiters <- struct{}{}:
		return o.wait(ctx)
	default:
		return errResponseOriginBusy
	}
}

func (o *responseOrigin) wait(ctx context.Context) error {
	defer func() { <-o.waiters }()
	deadline := time.Now().Add(o.timeout)
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errResponseOriginBusy
	case o.slots <- struct{}{}:
		// A ready slot can race a ready timer/cancellation in select. Recheck
		// before accepting it; expiration must not turn into new origin work.
		if err := originWaitingResult(ctx, deadline); err != nil {
			o.release()
			return err
		}
		return nil
	}
}

func originWaitingResult(ctx context.Context, deadline time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !time.Now().Before(deadline) {
		return errResponseOriginBusy
	}
	return nil
}

func (o *responseOrigin) release() { <-o.slots }
