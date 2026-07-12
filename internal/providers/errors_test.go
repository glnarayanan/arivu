package providers

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestSafeErrorClassificationDoesNotExposeTransportDetails(t *testing.T) {
	err := &url.Error{Op: "Post", URL: "https://provider.test/generate?key=super-secret", Err: context.DeadlineExceeded}
	if code := SafeErrorCode(err); code != ErrorProviderTimeout {
		t.Fatalf("code = %q", code)
	}
	message := SafeErrorMessage(err)
	if strings.Contains(message, "super-secret") || strings.Contains(message, "provider.test") || strings.Contains(message, context.DeadlineExceeded.Error()) {
		t.Fatalf("unsafe public message: %q", message)
	}
	if code := SafeErrorCode(errors.New("gemini status 429")); code != ErrorProviderRateLimited {
		t.Fatalf("rate limit code = %q", code)
	}
}
