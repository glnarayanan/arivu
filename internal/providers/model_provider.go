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
)

type ModelProvider struct {
	ID             string
	Name           string
	BaseURL        string
	DefaultModel   string
	Style          string
	APIKeyOptional bool
}

var modelProviders = []ModelProvider{
	{ID: ProviderOpenAI, Name: "OpenAI", BaseURL: "https://api.openai.com/v1", Style: ProviderStyleOpenAI},
	{ID: ProviderOpenRouter, Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", DefaultModel: "~openai/gpt-latest", Style: ProviderStyleOpenAI},
	{ID: ProviderXAI, Name: "xAI", BaseURL: "https://api.x.ai/v1", DefaultModel: "grok-4.5", Style: ProviderStyleOpenAI},
	{ID: ProviderGemini, Name: "Gemini", BaseURL: "https://generativelanguage.googleapis.com", DefaultModel: "gemini-2.5-flash", Style: ProviderStyleGemini},
	{ID: ProviderAnthropic, Name: "Anthropic", BaseURL: "https://api.anthropic.com", Style: ProviderStyleAnthropic},
	{ID: ProviderDeepSeek, Name: "DeepSeek", BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-v4-pro", Style: ProviderStyleOpenAI},
	{ID: ProviderMistral, Name: "Mistral", BaseURL: "https://api.mistral.ai/v1", DefaultModel: "mistral-large-latest", Style: ProviderStyleOpenAI},
	{ID: ProviderGroq, Name: "Groq", BaseURL: "https://api.groq.com/openai/v1", Style: ProviderStyleOpenAI},
	{ID: ProviderTogether, Name: "Together AI", BaseURL: "https://api.together.ai/v1", Style: ProviderStyleOpenAI},
	{ID: ProviderFireworks, Name: "Fireworks AI", BaseURL: "https://api.fireworks.ai/inference/v1", Style: ProviderStyleOpenAI},
	{ID: ProviderPerplexity, Name: "Perplexity", BaseURL: "https://api.perplexity.ai", DefaultModel: "sonar-pro", Style: ProviderStyleOpenAI},
	{ID: ProviderCerebras, Name: "Cerebras", BaseURL: "https://api.cerebras.ai/v1", Style: ProviderStyleOpenAI},
	{ID: ProviderZAI, Name: "Z.ai", BaseURL: "https://api.z.ai/api/paas/v4", DefaultModel: "glm-4.5", Style: ProviderStyleOpenAI},
	{ID: ProviderHuggingFace, Name: "Hugging Face", BaseURL: "https://router.huggingface.co/v1", Style: ProviderStyleOpenAI},
	{ID: ProviderLMStudio, Name: "LM Studio", BaseURL: "http://localhost:1234/v1", Style: ProviderStyleOpenAI, APIKeyOptional: true},
	{ID: ProviderOllama, Name: "Ollama/local", BaseURL: "http://localhost:11434/v1", Style: ProviderStyleOpenAI, APIKeyOptional: true},
	{ID: ProviderMiniMax, Name: "MiniMax", BaseURL: "https://api.minimax.io/v1", Style: ProviderStyleOpenAI},
	{ID: ProviderCustom, Name: "Custom", Style: ProviderStyleOpenAI, APIKeyOptional: true},
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
	case "hf", "hugging-face":
		return ProviderHuggingFace
	case "lm-studio":
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
