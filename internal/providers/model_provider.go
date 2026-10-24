package providers

import "strings"

const (
	ProviderAnthropic   = "anthropic"
	ProviderCerebras    = "cerebras"
	ProviderCustom      = "custom"
	ProviderDeepSeek    = "deepseek"
	ProviderFireworks   = "fireworks"
	ProviderGemini      = "gemini"
	ProviderGroq        = "groq"
	ProviderHuggingFace = "huggingface"
	ProviderLMStudio    = "lmstudio"
	ProviderMiniMax     = "minimax"
	ProviderMistral     = "mistral"
	ProviderOllama      = "ollama"
	ProviderOpenAI      = "openai"
	ProviderOpenRouter  = "openrouter"
	ProviderPerplexity  = "perplexity"
	ProviderTogether    = "together"
	ProviderXAI         = "xai"
	ProviderZAI         = "zai"

	ProviderStyleAnthropic = "anthropic"
	ProviderStyleGemini    = "gemini"
	ProviderStyleOpenAI    = "openai"

	EmbeddingStyleGemini = "gemini"
	EmbeddingStyleOpenAI = "openai"
)

type ModelProvider struct {
	ID             string
	Name           string
	BaseURL        string
	DefaultModel   string
	Style          string
	APIKeyOptional bool
	// Embedding describes the provider's text-embedding API. A zero value
	// (empty Style) means the provider is text-generation only.
	Embedding EmbeddingSupport
}

// EmbeddingSupport records whether and how a preset exposes an embeddings API.
// Capabilities were checked against each provider's public API docs; see
// openwiki/operations/deployment.md ("Embedding model") for the matrix.
type EmbeddingSupport struct {
	// Style is EmbeddingStyleGemini, EmbeddingStyleOpenAI, or empty for
	// text-only providers.
	Style string
	// BaseURL overrides the text-generation base URL when the embeddings
	// endpoint lives elsewhere (for example Perplexity's /v1 prefix).
	BaseURL string
	// DefaultModel is used when the administrator does not pick a model.
	DefaultModel string
	// DefaultDimensions is requested when the administrator does not choose
	// dimensions. Zero means the model's native size.
	DefaultDimensions int
	// DimensionsParam is the request field that selects output dimensions,
	// or empty when the API offers no such choice.
	DimensionsParam string
	// Base64Int8 marks APIs that return base64-encoded signed int8 vectors
	// instead of float arrays.
	Base64Int8 bool
	// Note explains limitations shown in Settings.
	Note string
}

func (p ModelProvider) SupportsEmbeddings() bool {
	return p.Embedding.Style != ""
}

var modelProviders = []ModelProvider{
	{ID: ProviderOpenAI, Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Style: ProviderStyleOpenAI, Embedding: EmbeddingSupport{Style: EmbeddingStyleOpenAI, DefaultModel: "text-embedding-3-small", DimensionsParam: "dimensions"}},
	{ID: ProviderOpenRouter, Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", DefaultModel: "~openai/gpt-latest", Style: ProviderStyleOpenAI, Embedding: EmbeddingSupport{Style: EmbeddingStyleOpenAI, DefaultModel: "openai/text-embedding-3-small", DimensionsParam: "dimensions"}},
	{ID: ProviderXAI, Name: "xAI", BaseURL: "https://api.x.ai/v1", DefaultModel: "grok-4.5", Style: ProviderStyleOpenAI, Embedding: EmbeddingSupport{Note: "xAI does not offer an embeddings API."}},
	{ID: ProviderGemini, Name: "Gemini", BaseURL: "https://generativelanguage.googleapis.com", DefaultModel: "gemini-2.5-flash", Style: ProviderStyleGemini, Embedding: EmbeddingSupport{Style: EmbeddingStyleGemini, DefaultModel: GeminiEmbeddingModel, DefaultDimensions: GeminiEmbeddingDimensions, DimensionsParam: "output_dimensionality"}},
	{ID: ProviderAnthropic, Name: "Anthropic", BaseURL: "https://api.anthropic.com", Style: ProviderStyleAnthropic, Embedding: EmbeddingSupport{Note: "Anthropic does not offer an embeddings API."}},
	{ID: ProviderDeepSeek, Name: "DeepSeek", BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-v4-pro", Style: ProviderStyleOpenAI, Embedding: EmbeddingSupport{Note: "DeepSeek does not offer an embeddings API."}},
	{ID: ProviderMistral, Name: "Mistral", BaseURL: "https://api.mistral.ai/v1", DefaultModel: "mistral-large-latest", Style: ProviderStyleOpenAI, Embedding: EmbeddingSupport{Style: EmbeddingStyleOpenAI, DefaultModel: "mistral-embed", DimensionsParam: "output_dimension", Note: "mistral-embed has a fixed size; codestral-embed accepts a dimension choice."}},
	{ID: ProviderGroq, Name: "Groq", BaseURL: "https://api.groq.com/openai/v1", Style: ProviderStyleOpenAI, Embedding: EmbeddingSupport{Note: "Groq does not offer an embeddings API."}},
	{ID: ProviderTogether, Name: "Together AI", BaseURL: "https://api.together.ai/v1", Style: ProviderStyleOpenAI, Embedding: EmbeddingSupport{Note: "Together AI no longer offers serverless embedding models. Use Custom with a dedicated endpoint."}},
	{ID: ProviderFireworks, Name: "Fireworks AI", BaseURL: "https://api.fireworks.ai/inference/v1", Style: ProviderStyleOpenAI, Embedding: EmbeddingSupport{Style: EmbeddingStyleOpenAI, DefaultModel: "nomic-ai/nomic-embed-text-v1.5", DimensionsParam: "dimensions"}},
	{ID: ProviderPerplexity, Name: "Perplexity", BaseURL: "https://api.perplexity.ai", DefaultModel: "sonar-pro", Style: ProviderStyleOpenAI, Embedding: EmbeddingSupport{Style: EmbeddingStyleOpenAI, BaseURL: "https://api.perplexity.ai/v1", DefaultModel: "pplx-embed-v1-0.6b", DimensionsParam: "dimensions", Base64Int8: true}},
	{ID: ProviderCerebras, Name: "Cerebras", BaseURL: "https://api.cerebras.ai/v1", Style: ProviderStyleOpenAI, Embedding: EmbeddingSupport{Note: "Cerebras does not offer an embeddings API."}},
	{ID: ProviderZAI, Name: "Z.ai", BaseURL: "https://api.z.ai/api/paas/v4", DefaultModel: "glm-4.5", Style: ProviderStyleOpenAI, Embedding: EmbeddingSupport{Note: "The international Z.ai API does not document an embeddings endpoint."}},
	{ID: ProviderHuggingFace, Name: "Hugging Face", BaseURL: "https://router.huggingface.co/v1", Style: ProviderStyleOpenAI, Embedding: EmbeddingSupport{Note: "The Hugging Face router's OpenAI-compatible API supports chat only. Use Custom with an OpenAI-compatible embeddings endpoint."}},
	{ID: ProviderLMStudio, Name: "LM Studio", BaseURL: "http://localhost:1234/v1", Style: ProviderStyleOpenAI, APIKeyOptional: true, Embedding: EmbeddingSupport{Style: EmbeddingStyleOpenAI, Note: "Load an embedding model in LM Studio and enter its model id."}},
	{ID: ProviderOllama, Name: "Ollama/local", BaseURL: "http://localhost:11434/v1", Style: ProviderStyleOpenAI, APIKeyOptional: true, Embedding: EmbeddingSupport{Style: EmbeddingStyleOpenAI, DefaultModel: "embeddinggemma", DimensionsParam: "dimensions"}},
	{ID: ProviderMiniMax, Name: "MiniMax", BaseURL: "https://api.minimax.io/v1", Style: ProviderStyleOpenAI, Embedding: EmbeddingSupport{Note: "MiniMax does not document an embeddings API."}},
	{ID: ProviderCustom, Name: "Custom", Style: ProviderStyleOpenAI, APIKeyOptional: true, Embedding: EmbeddingSupport{Style: EmbeddingStyleOpenAI, DimensionsParam: "dimensions", Note: "Any OpenAI-compatible POST /embeddings endpoint."}},
}

func ModelProviders() []ModelProvider {
	result := make([]ModelProvider, len(modelProviders))
	copy(result, modelProviders)
	return result
}

func ModelProviderDefinition(id string) ModelProvider {
	normalized := NormalizeModelProvider(id)
	for _, provider := range modelProviders {
		if provider.ID == normalized {
			return provider
		}
	}
	return modelProviders[len(modelProviders)-1]
}

func NormalizeModelProvider(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	switch normalized {
	case "", "gemini", "google", "google-gemini":
		return ProviderGemini
	case "anthropic", "claude":
		return ProviderAnthropic
	case "cerebras":
		return ProviderCerebras
	case "deepseek", "deep-seek":
		return ProviderDeepSeek
	case "fireworks", "fireworks-ai":
		return ProviderFireworks
	case "groq":
		return ProviderGroq
	case "hf", "huggingface", "hugging-face":
		return ProviderHuggingFace
	case "lmstudio", "lm-studio":
		return ProviderLMStudio
	case "minimax", "mini-max":
		return ProviderMiniMax
	case "mistral", "mistral-ai":
		return ProviderMistral
	case "ollama", "local":
		return ProviderOllama
	case "openai":
		return ProviderOpenAI
	case "openrouter", "open-router":
		return ProviderOpenRouter
	case "perplexity":
		return ProviderPerplexity
	case "together", "together-ai":
		return ProviderTogether
	case "xai", "x-ai", "x.ai":
		return ProviderXAI
	case "zai", "z-ai", "z.ai", "zhipu", "zhipu-ai":
		return ProviderZAI
	case "custom", "opencode", "open-code":
		return ProviderCustom
	default:
		return ProviderCustom
	}
}
