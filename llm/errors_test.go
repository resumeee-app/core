package llm_test

import (
	"errors"
	"testing"

	"github.com/resumeee-app/core"
	"github.com/resumeee-app/core/llm"
)

func TestSentinelErrorClassification(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		coreMatch error
		wantClass core.ErrorClass
		wantRetry bool
	}{
		{
			name:      "ErrBadRequest is permanent",
			err:       llm.ErrBadRequest,
			coreMatch: core.ErrPermanentFailure,
			wantClass: core.ErrorClassPermanent,
			wantRetry: false,
		},
		{
			name:      "ErrUnauthorized matches core.ErrUnauthorized and permanent",
			err:       llm.ErrUnauthorized,
			coreMatch: core.ErrUnauthorized,
			wantClass: core.ErrorClassPermanent,
			wantRetry: false,
		},
		{
			name:      "ErrUpstreamQuota matches core.ErrUpstreamQuota and non-retryable",
			err:       llm.ErrUpstreamQuota,
			coreMatch: core.ErrUpstreamQuota,
			wantClass: core.ErrorClassUpstreamQuota,
			wantRetry: false,
		},
		{
			name:      "ErrInvalidResponse is permanent",
			err:       llm.ErrInvalidResponse,
			coreMatch: core.ErrPermanentFailure,
			wantClass: core.ErrorClassPermanent,
			wantRetry: false,
		},
		{
			name:      "ErrProviderNotFound is permanent",
			err:       llm.ErrProviderNotFound,
			coreMatch: core.ErrPermanentFailure,
			wantClass: core.ErrorClassPermanent,
			wantRetry: false,
		},
		{
			name:      "ErrNoProviders is permanent",
			err:       llm.ErrNoProviders,
			coreMatch: core.ErrPermanentFailure,
			wantClass: core.ErrorClassPermanent,
			wantRetry: false,
		},
		{
			name:      "ErrRateLimited is transient",
			err:       llm.ErrRateLimited,
			coreMatch: core.ErrTransientFailure,
			wantClass: core.ErrorClassTransient,
			wantRetry: true,
		},
		{
			name:      "ErrUpstream is transient",
			err:       llm.ErrUpstream,
			coreMatch: core.ErrTransientFailure,
			wantClass: core.ErrorClassTransient,
			wantRetry: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.coreMatch) {
				t.Errorf("expected errors.Is(%v, %v) to be true", tc.err, tc.coreMatch)
			}
			if got := core.ClassifyError(tc.err); got != tc.wantClass {
				t.Errorf("ClassifyError(%v) = %q, want %q", tc.err, got, tc.wantClass)
			}
			if got := core.IsRetryableError(tc.err); got != tc.wantRetry {
				t.Errorf("IsRetryableError(%v) = %v, want %v", tc.err, got, tc.wantRetry)
			}
		})
	}
}
