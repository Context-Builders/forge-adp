// Package llm provides a unified interface for interacting with large language
// model providers. Each sub-package implements the Provider interface for a
// specific backend (OpenAI, AWS Bedrock, Azure OpenAI, Google Vertex AI,
// Ollama, Groq, and Mistral).
package llm

import "context"

// Role constants for chat message participants.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message represents a single turn in a conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CompletionRequest holds parameters for a chat completion call.
type CompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	TopP        float64   `json:"top_p,omitempty"`
	Stop        []string  `json:"stop,omitempty"`
}

// CompletionResponse is the normalised response returned by every provider.
type CompletionResponse struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice is a single generated completion within a response.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage reports the number of tokens consumed by a request.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Provider is the common interface implemented by every LLM backend.
type Provider interface {
	// Complete sends a chat completion request and returns the response.
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
	// Name returns a stable, lower-case identifier for the provider.
	Name() string
	// Models returns the set of model identifiers supported by this provider.
	Models() []string
}
