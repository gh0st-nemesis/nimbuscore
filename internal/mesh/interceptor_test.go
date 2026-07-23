package mesh

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestClientInterceptorRetriesRetryableErrors(t *testing.T) {
	var calls int32
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return status.Error(codes.Unavailable, "transient")
		}
		return nil
	}

	interceptor := NewClientInterceptor(RetryPolicy{
		MaxAttempts:       5,
		InitialBackoff:    time.Millisecond,
		BackoffMultiplier: 1,
		RetryableCodes:    []codes.Code{codes.Unavailable},
	}, NewCircuitBreaker(10, time.Second))

	err := interceptor(context.Background(), "/svc/Method", nil, nil, nil, invoker)
	if err != nil {
		t.Fatalf("interceptor returned error after eventual success: %v", err)
	}
	if calls != 3 {
		t.Errorf("invoker called %d times, want 3", calls)
	}
}

func TestClientInterceptorDoesNotRetryNonRetryableErrors(t *testing.T) {
	var calls int32
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		atomic.AddInt32(&calls, 1)
		return status.Error(codes.InvalidArgument, "bad request")
	}

	interceptor := NewClientInterceptor(RetryPolicy{
		MaxAttempts:       5,
		InitialBackoff:    time.Millisecond,
		BackoffMultiplier: 1,
		RetryableCodes:    []codes.Code{codes.Unavailable},
	}, NewCircuitBreaker(10, time.Second))

	err := interceptor(context.Background(), "/svc/Method", nil, nil, nil, invoker)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("got error %v, want InvalidArgument", err)
	}
	if calls != 1 {
		t.Errorf("invoker called %d times, want 1 (non-retryable)", calls)
	}
}

func TestClientInterceptorStopsCallingAfterCircuitOpens(t *testing.T) {
	var calls int32
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		atomic.AddInt32(&calls, 1)
		return status.Error(codes.Unavailable, "down")
	}

	breaker := NewCircuitBreaker(2, time.Hour)
	interceptor := NewClientInterceptor(RetryPolicy{
		MaxAttempts:       1,
		InitialBackoff:    time.Millisecond,
		BackoffMultiplier: 1,
	}, breaker)

	for range 2 {
		_ = interceptor(context.Background(), "/svc/Method", nil, nil, nil, invoker)
	}
	if breaker.State() != Open {
		t.Fatalf("breaker state = %v, want Open after 2 failures (threshold=2)", breaker.State())
	}

	before := calls
	err := interceptor(context.Background(), "/svc/Method", nil, nil, nil, invoker)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("got %v, want Unavailable (circuit open)", err)
	}
	if calls != before {
		t.Errorf("invoker was called while circuit was open: calls went from %d to %d", before, calls)
	}
}

func TestCircuitBreakerHalfOpenAfterResetTimeout(t *testing.T) {
	cb := NewCircuitBreaker(1, 20*time.Millisecond)
	cb.RecordFailure()
	if cb.State() != Open {
		t.Fatalf("state = %v, want Open", cb.State())
	}
	if cb.Allow() {
		t.Fatal("Allow returned true immediately after opening")
	}

	time.Sleep(30 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("Allow returned false after reset timeout elapsed, want a half-open trial")
	}
	if cb.State() != HalfOpen {
		t.Errorf("state = %v, want HalfOpen", cb.State())
	}

	cb.RecordSuccess()
	if cb.State() != Closed {
		t.Errorf("state after success in half-open = %v, want Closed", cb.State())
	}
}

func TestCircuitBreakerHalfOpenFailureReopens(t *testing.T) {
	cb := NewCircuitBreaker(1, 10*time.Millisecond)
	cb.RecordFailure()
	time.Sleep(20 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("expected half-open trial to be allowed")
	}
	cb.RecordFailure()
	if cb.State() != Open {
		t.Errorf("state after half-open failure = %v, want Open", cb.State())
	}
}
