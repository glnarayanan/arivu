package app

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/glnarayanan/arivu/internal/providers"
)

func TestProviderUsageStoresOnlySafeErrors(t *testing.T) {
	usage := newProviderUsage()
	usage.RecordAI("summary", &url.Error{Op: "Post", URL: "https://provider.test?key=super-secret", Err: context.DeadlineExceeded})
	item := usage.Snapshot()["ai"].(map[string]providerUsageOp)["summary"]
	if item.LastErrorCode != providers.ErrorProviderTimeout {
		t.Fatalf("last error code = %q", item.LastErrorCode)
	}
	if strings.Contains(item.LastError, "super-secret") || strings.Contains(item.LastError, "provider.test") || strings.Contains(item.LastError, context.DeadlineExceeded.Error()) {
		t.Fatalf("unsafe provider usage error: %q", item.LastError)
	}
}
