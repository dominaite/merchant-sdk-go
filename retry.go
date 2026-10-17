package dominaite

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"
)

const (
	// defaultMaxRetries is how many times a client retries a failed call
	// after the first attempt, unless WithMaxRetries says otherwise.
	defaultMaxRetries = 2
	// retryBaseDelay and retryMaxDelay bound the full-jitter backoff: retry n
	// (0 for the first) waits a random time in [0, min(retryMaxDelay,
	// retryBaseDelay * 2^n)).
	retryBaseDelay = 500 * time.Millisecond
	retryMaxDelay  = 8 * time.Second
)

// WithMaxRetries sets how many times the client retries a failed call after
// the first attempt. Defaults to 2, so a call is sent at most 3 times. 0
// disables retries. A negative value makes New return a *ValidationError.
//
// The client retries reads (every GET) and every POST that carries an
// idempotency key, except CreateWebhookEndpoint, which is never retried. It
// retries only a *TransportError (a network failure or a 5xx) and
// PAYMENT_PROCESSING_UNAVAILABLE, on the refusal and on the charge error, plus
// the charge route's 503 PAYMENT_METHOD_CHARGES_DISABLED. Every retry sends the
// same idempotency key and the same body, signed again with a fresh timestamp.
// A 429 is never retried. Neither is a DELETE or a PATCH.
//
// The wait before retry n (0 for the first) is a random time between 0 and
// 500ms * 2^n, capped at 8 seconds. The timeout applies to each attempt, so
// one call can take up to (maxRetries + 1) times the timeout plus those waits.
// Bound the whole call with a context deadline when that matters.
func WithMaxRetries(maxRetries int) Option {
	return func(c *Client) {
		if maxRetries < 0 {
			c.optionErr = newValidationError("maxRetries must not be negative")
			return
		}
		c.maxRetries = maxRetries
	}
}

// retryPolicy is one call's retry budget and its name in the retry warning.
type retryPolicy struct {
	operation  string
	maxRetries int
	baseDelay  time.Duration
}

// retryPolicy is the client's policy for one operation.
func (c *Client) retryPolicy(operation string) retryPolicy {
	return retryPolicy{operation: operation, maxRetries: c.maxRetries, baseDelay: retryBaseDelay}
}

// retry runs attempt until it succeeds, fails with an error that is not
// retryable, the context ends or the policy runs out. attempt must send the
// same idempotency key and the same body every time; send signs each attempt
// again with the current timestamp.
func retry[T any](ctx context.Context, c *Client, policy retryPolicy, attempt func() (T, error)) (T, error) {
	for n := 0; ; n++ {
		result, err := attempt()
		if err == nil || n >= policy.maxRetries || !isRetryable(err) || ctx.Err() != nil {
			return result, err
		}
		c.warn(ctx, fmt.Sprintf("dominaite-go: retrying %s after %s (attempt %d of %d)",
			policy.operation, retryReason(err), n+2, policy.maxRetries+1))
		if sleepErr := c.sleep(ctx, c.retryDelay(policy.baseDelay, n)); sleepErr != nil {
			var zero T
			return zero, newTransportError("Retrying was cancelled: "+sleepErr.Error(), sleepErr)
		}
	}
}

// retryDelay is the full-jitter wait before retry n (0 for the first): a
// random time in [0, min(retryMaxDelay, base * 2^n)).
func (c *Client) retryDelay(base time.Duration, n int) time.Duration {
	bound := retryMaxDelay
	if n < 30 {
		if scaled := base << n; scaled > 0 && scaled < bound {
			bound = scaled
		}
	}
	return time.Duration(c.random() * float64(bound))
}

// isRetryable names the failures the client retries. Each leaves nothing
// done, or is answered the same way again under the same idempotency key: a
// transport failure or 5xx, card processing being unavailable right now, and
// charges switched off for the moment. A charge outcome that is unknown or
// failed is not retried; it is a *ChargeError to reconcile.
func isRetryable(err error) bool {
	var transportErr *TransportError
	if errors.As(err, &transportErr) {
		return true
	}
	var refusal *RefusalError
	if errors.As(err, &refusal) {
		return refusal.ErrorCode == ErrorCodePaymentProcessingUnavailable
	}
	var chargeErr *ChargeError
	if errors.As(err, &chargeErr) {
		return chargeErr.ErrorCode == ChargeErrorProcessingUnavailable ||
			chargeErr.ErrorCode == ChargeErrorChargesDisabled
	}
	return false
}

// sleepContext waits d, or until ctx ends, whichever comes first.
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// randomFloat is rand.Float64. The top-level source is seeded and safe for
// concurrent use.
func randomFloat() float64 { return rand.Float64() }
