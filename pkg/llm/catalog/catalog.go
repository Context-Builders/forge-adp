package catalog

import (
	"github.com/dotrage/forge-adp/pkg/llm"
	"github.com/dotrage/forge-adp/pkg/llm/azureopenai"
	"github.com/dotrage/forge-adp/pkg/llm/bedrock"
	"github.com/dotrage/forge-adp/pkg/llm/groq"
	"github.com/dotrage/forge-adp/pkg/llm/mistral"
	"github.com/dotrage/forge-adp/pkg/llm/ollama"
	"github.com/dotrage/forge-adp/pkg/llm/openai"
	"github.com/dotrage/forge-adp/pkg/llm/vertex"
)

// ProviderInfo holds static metadata about an LLM provider.
type ProviderInfo struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Models      []string `json:"models"`
}

// KnownProviders returns metadata for every built-in LLM provider.
// No credentials are required; this is purely informational.
func KnownProviders() []ProviderInfo {
	providers := []llm.Provider{
		openai.New(openai.Config{}),
		azureopenai.New(azureopenai.Config{}),
		bedrock.New(bedrock.Config{}),
		ollama.New(ollama.Config{}),
		groq.New(groq.Config{}),
		mistral.New(mistral.Config{}),
	}

	infos := make([]ProviderInfo, 0, len(providers)+1)
	for _, p := range providers {
		infos = append(infos, ProviderInfo{
			Name:        p.Name(),
			DisplayName: displayName(p.Name()),
			Models:      p.Models(),
		})
	}

	// Vertex AI exposes its model list without requiring credentials.
	infos = append(infos, ProviderInfo{
		Name:        "vertex",
		DisplayName: "Google Vertex AI / Gemini",
		Models:      vertex.KnownModels(),
	})

	return infos
}

func displayName(name string) string {
	switch name {
	case "openai":
		return "OpenAI"
	case "azureopenai":
		return "Azure OpenAI"
	case "bedrock":
		return "AWS Bedrock"
	case "ollama":
		return "Ollama"
	case "groq":
		return "Groq"
	case "mistral":
		return "Mistral AI"
	default:
		return name
	}
}
