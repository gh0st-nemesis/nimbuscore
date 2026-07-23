package mesh

import (
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type RetryPolicy struct {
	MaxAttempts       int
	InitialBackoff    time.Duration
	MaxBackoff        time.Duration
	BackoffMultiplier float64
	RetryableCodes    []codes.Code
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:       3,
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        2 * time.Second,
		BackoffMultiplier: 2,
		RetryableCodes:    []codes.Code{codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted},
	}
}

func (p RetryPolicy) retryable(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	return slices.Contains(p.RetryableCodes, st.Code())
}

func (p RetryPolicy) attempts() int {
	if p.MaxAttempts <= 0 {
		return 1
	}
	return p.MaxAttempts
}
