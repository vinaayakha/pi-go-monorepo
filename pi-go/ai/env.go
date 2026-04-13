package ai

import "os"

// envKeyMap maps provider names to environment variable names.
var envKeyMap = map[Provider][]string{
	ProviderAnthropic:     {"ANTHROPIC_API_KEY"},
	ProviderOpenAI:        {"OPENAI_API_KEY"},
	ProviderGoogle:        {"GOOGLE_API_KEY", "GEMINI_API_KEY"},
	ProviderXAI:           {"XAI_API_KEY"},
	ProviderGroq:          {"GROQ_API_KEY"},
	ProviderMistral:       {"MISTRAL_API_KEY"},
	ProviderOpenRouter:    {"OPENROUTER_API_KEY"},
	ProviderAmazonBedrock: {"AWS_ACCESS_KEY_ID"},
	"azure-openai":        {"AZURE_OPENAI_API_KEY"},
}

// GetEnvAPIKey returns the first matching API key from environment variables.
func GetEnvAPIKey(provider Provider) string {
	keys, ok := envKeyMap[provider]
	if !ok {
		return ""
	}
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}
