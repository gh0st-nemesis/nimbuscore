package mesh

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func NewClientInterceptor(retry RetryPolicy, breaker *CircuitBreaker) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if !breaker.Allow() {
			return status.Errorf(codes.Unavailable, "mesh: circuit breaker open for %s", method)
		}

		backoff := retry.InitialBackoff
		attempts := retry.attempts()

		var lastErr error
		for attempt := range attempts {
			lastErr = invoker(ctx, method, req, reply, cc, opts...)
			if lastErr == nil {
				breaker.RecordSuccess()
				return nil
			}
			if attempt == attempts-1 || !retry.retryable(lastErr) {
				break
			}

			select {
			case <-ctx.Done():
				breaker.RecordFailure()
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff = time.Duration(float64(backoff) * retry.BackoffMultiplier)
			if retry.MaxBackoff > 0 && backoff > retry.MaxBackoff {
				backoff = retry.MaxBackoff
			}
		}

		breaker.RecordFailure()
		return lastErr
	}
}
