package app

import (
	"context"
	"log"
	"net/http"

	"github.com/glnarayanan/arivu/internal/auth"
	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/runtimeconfig"
)

// embeddingAffectedFeatures lists what stops working without an embedding
// model. The Settings and API Usage pages show it verbatim.
var embeddingAffectedFeatures = []string{
	"Semantic ranking in Graph search (keyword matching still works)",
	"Related items on a bookmark",
	"Similarity links in the Graph",
	"Similar-content duplicate detection (exact URL duplicates still work)",
}

func (a *App) effectiveSettings(ctx context.Context) runtimeconfig.Effective {
	if effective, err := a.runtime.Effective(ctx); err == nil {
		return effective
	}
	return runtimeconfig.FromConfig(a.cfg)
}

func (a *App) embeddingClient(ctx context.Context) providers.EmbeddingClient {
	return providers.EmbeddingClient{Config: a.effectiveSettings(ctx).Embedding(), Recorder: a.usage.RecordAI}
}

func (a *App) scheduleReembed(ctx context.Context) {
	if _, err := a.bookmarks.ScheduleReembed(ctx); err != nil {
		log.Printf("embeddings: schedule re-embed: %v", err)
	}
}

// embeddingStatus describes the effective embedding configuration without
// secrets, plus progress of the background re-embedding pass.
func (a *App) embeddingStatus(ctx context.Context) (map[string]any, error) {
	effective := a.effectiveSettings(ctx)
	resolved := effective.Embedding()
	textProvider := providers.ModelProviderDefinition(effective.AIProvider)
	stats, err := a.bookmarks.EmbeddingStats(ctx)
	if err != nil {
		return nil, err
	}
	presets := []map[string]any{}
	for _, provider := range providers.ModelProviders() {
		presets = append(presets, map[string]any{
			"id":                   provider.ID,
			"name":                 provider.Name,
			"supports_embeddings":  provider.SupportsEmbeddings(),
			"default_model":        provider.Embedding.DefaultModel,
			"default_dimensions":   provider.Embedding.DefaultDimensions,
			"dimensions_supported": provider.Embedding.DimensionsParam != "",
			"base_url":             embeddingPresetBaseURL(provider),
			"api_key_optional":     provider.APIKeyOptional,
			"note":                 provider.Embedding.Note,
		})
	}
	status := map[string]any{
		"configured":                        resolved.Configured(),
		"mode":                              resolved.Mode,
		"provider":                          resolved.Provider,
		"provider_name":                     providers.ModelProviderDefinition(resolved.Provider).Name,
		"model":                             resolved.Model,
		"dimensions":                        resolved.Dimensions,
		"base_url":                          resolved.BaseURL,
		"space":                             resolved.Space(),
		"reason":                            resolved.Reason,
		"message":                           resolved.ReasonMessage(),
		"text_provider":                     textProvider.ID,
		"text_provider_name":                textProvider.Name,
		"text_provider_supports_embeddings": textProvider.SupportsEmbeddings(),
		"affected_features":                 embeddingAffectedFeatures,
		"vectors":                           stats,
		"providers":                         presets,
	}
	if resolved.Mode == providers.EmbeddingProviderNone {
		status["provider"] = ""
		status["provider_name"] = ""
	}
	return status, nil
}

func embeddingPresetBaseURL(provider providers.ModelProvider) string {
	if !provider.SupportsEmbeddings() {
		return ""
	}
	if provider.Embedding.BaseURL != "" {
		return provider.Embedding.BaseURL
	}
	return provider.BaseURL
}

func (a *App) adminEmbeddings(w http.ResponseWriter, r *http.Request, user auth.User) {
	status, err := a.embeddingStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not load embedding status")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *App) adminReembed(w http.ResponseWriter, r *http.Request, user auth.User) {
	if !a.embeddingClient(r.Context()).Configured() {
		writeError(w, http.StatusConflict, "Configure an embedding model before re-embedding")
		return
	}
	queued, err := a.bookmarks.ScheduleReembed(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not schedule re-embedding")
		return
	}
	a.auditEvent(r.Context(), user.ID, "admin.embeddings.reembed", "settings", "", map[string]any{"queued": queued})
	writeJSON(w, http.StatusOK, map[string]any{"status": map[bool]string{true: "queued", false: "not_needed"}[queued], "queued": queued})
}
