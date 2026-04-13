package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/vinaayakha/pi-go/agent"
	"github.com/vinaayakha/pi-go/ai"
	"github.com/vinaayakha/pi-go/ai/providers"
	"github.com/vinaayakha/pi-go/threads"
	"github.com/vinaayakha/pi-go/tools"
)

func main() {
	providers.RegisterBuiltins()

	// Pick provider based on available API keys
	model := pickModel()
	cwd, _ := os.Getwd()

	// Build system prompt with project context
	contextFiles := tools.LoadProjectContextFiles(cwd)
	allToolNames := []string{"read", "bash", "edit", "write", "grep", "find", "ls"}
	systemPrompt := tools.BuildSystemPrompt(tools.BuildSystemPromptOptions{
		SelectedTools: allToolNames,
		ToolSnippets:  tools.DefaultToolSnippets(),
		PromptGuidelines: []string{
			"Use read to examine files instead of cat or sed.",
			"Use edit for precise changes (edits[].oldText must match exactly)",
			"Use write only for new files or complete rewrites.",
		},
		Cwd:          cwd,
		ContextFiles: contextFiles,
	})

	// Create agent with thread persistence
	a := agent.NewAgent(model)
	a.SystemPrompt = systemPrompt
	a.SetTools(tools.AllTools(cwd))
	a.ThreadStore = threads.NewMemoryStore()

	// Subscribe to events
	a.Subscribe(func(event agent.AgentEvent) {
		switch event.Type {
		case agent.MessageEventUpdate:
			if event.AssistantMessageEvent != nil && event.AssistantMessageEvent.Type == ai.EventTextDelta {
				fmt.Print(event.AssistantMessageEvent.Delta)
			}
		case agent.ToolExecEventStart:
			fmt.Printf("\n[tool: %s]\n", event.ToolName)
		case agent.AgentEventEnd:
			fmt.Println("\n--- done ---")
		}
	})

	// Get prompt
	prompt := "Hello! What can you help me with?"
	if len(os.Args) > 1 {
		prompt = strings.Join(os.Args[1:], " ")
	}

	// Create a thread for this conversation
	threadID, _ := a.NewThread(map[string]string{"source": "cli"})
	fmt.Printf("[thread: %s]\n", threadID)

	ctx := context.Background()
	if err := a.Prompt(ctx, prompt); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	a.WaitForIdle()
}

func pickModel() ai.Model {
	// Try Anthropic first
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return ai.Model{
			ID: "claude-sonnet-4-20250514", Name: "Claude Sonnet 4",
			API: ai.APIAnthropicMessages, Provider: ai.ProviderAnthropic,
			BaseURL: "https://api.anthropic.com", Reasoning: true,
			Input: []string{"text", "image"}, Cost: ai.ModelCost{Input: 3.0, Output: 15.0},
			ContextWindow: 200000, MaxTokens: 8192,
		}
	}

	// Try OpenAI Responses API
	if os.Getenv("OPENAI_API_KEY") != "" {
		return ai.Model{
			ID: "gpt-4o", Name: "GPT-4o",
			API: ai.APIOpenAIResponses, Provider: ai.ProviderOpenAI,
			BaseURL: "https://api.openai.com", Reasoning: false,
			Input: []string{"text", "image"}, Cost: ai.ModelCost{Input: 2.5, Output: 10.0},
			ContextWindow: 128000, MaxTokens: 16384,
		}
	}

	// Try Google
	if os.Getenv("GOOGLE_API_KEY") != "" || os.Getenv("GEMINI_API_KEY") != "" {
		return ai.Model{
			ID: "gemini-2.0-flash", Name: "Gemini 2.0 Flash",
			API: ai.APIGoogleGenerativeAI, Provider: ai.ProviderGoogle,
			BaseURL: "https://generativelanguage.googleapis.com", Reasoning: false,
			Input: []string{"text", "image"}, Cost: ai.ModelCost{Input: 0.075, Output: 0.3},
			ContextWindow: 1048576, MaxTokens: 8192,
		}
	}

	// Try Mistral
	if os.Getenv("MISTRAL_API_KEY") != "" {
		return ai.Model{
			ID: "mistral-large-latest", Name: "Mistral Large",
			API: ai.APIMistralConversations, Provider: ai.ProviderMistral,
			BaseURL: "https://api.mistral.ai", Reasoning: false,
			Input: []string{"text"}, Cost: ai.ModelCost{Input: 2.0, Output: 6.0},
			ContextWindow: 128000, MaxTokens: 8192,
		}
	}

	fmt.Fprintln(os.Stderr, "No API key found. Set ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_API_KEY, or MISTRAL_API_KEY.")
	os.Exit(1)
	return ai.Model{}
}
