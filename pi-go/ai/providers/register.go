package providers

// RegisterBuiltins registers all built-in API providers.
func RegisterBuiltins() {
	RegisterAnthropic()
	RegisterOpenAICompletions()
	RegisterOpenAIResponses()
	RegisterGoogle()
	RegisterMistral()
}
