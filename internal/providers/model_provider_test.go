package providers

import "testing"

func TestNormalizeModelProviderAcceptsEveryPresetID(t *testing.T) {
	for _, provider := range ModelProviders() {
		if got := NormalizeModelProvider(provider.ID); got != provider.ID {
			t.Errorf("NormalizeModelProvider(%q) = %q", provider.ID, got)
		}
	}
}
