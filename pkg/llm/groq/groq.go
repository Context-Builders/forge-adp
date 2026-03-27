// Package groq implements llm.Provider for Groq's OpenAI-compatible inference API.
package groq

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dotrage/forge-adp/pkg/llm"
)

const (
	ModelLlama33_70b    = "llama-3.3-70b-versatile"
	ModelLlama31_70b    = "llama-3.1-70b-versatile"
	ModelLlama31_8b     = "llama-3.1-8b-instant"
	ModelLlama3_70b     = "llama3-70b-8192"
	ModelLlama3_8b      = "llama3-8b-8192"
	ModelMixtral8x7b    = "mixtral-8x7b-32768"
	ModelGemma2_9b      = "gemma2-9b-it"
	ModelDeepSeekR1_70b = "deepseek-r1-distill-llama-70b"
)

const defaultBaseURL = "https://api.groq.com"

// Config holds credentials for the Groq provider.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider implements llm.Provider for Groq.
type Provider struct {
	cfg    Config
	client *http.Client
}

// New returns a configured Groq Provider.
func New(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	cl := cfg.HTTPClient
	if cl == nil {
		cl = &http.Client{Timeout: 120 * time.Second}
	}
	return &Provider{cfg: cfg, client: cl}
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "groq" }

// Models returns the supported model identifiers.
func (p *Provider) Models() []string {
	return []string{
		ModelLlama33_70b, ModelLlama31_70b, ModelLlama31_8b,
		ModelLlama3_70b, ModelLlama3_8b,
		ModelMixtral8x7b, ModelGemma2_9b,
		ModelDeepSeekR1_70b,
	}
}

type groqRequest struct {
	Model       string    `json:"model"`
	Messages    []groqMsg `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	TopP        float64   `json:"top_p,omitempty"`
	Stop        []string  `json:"stop,omitempty"`
}

type groqMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type groqResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int     `json:"index"`
		Message      groqMsg `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Complete calls the Groq Chat Completions endpoint.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	msgs := make([]groqMsg, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = groqMsg{Role: m.Role, Content: m.Content}
	}
	body, err := json.Marshal(groqRequest{
		Model: req.Model, Messages: msgs,
		MaxTokens: req.MaxTokens, Temperature: req.Temperature,
		TopP: req.TopP, Stop: req.Stop,
	})
	if err != nil {
		return nil, fmt.Errorf("groq: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseURL+"/openai/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("groq: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("groq: http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("groq: read body: %w", err)
	}
	var ar groqResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("groq: decode: %w", err)
	}
	if ar.Error != nil {
		return nil, fmt.Errorf("groq: %s (type=%s)", ar.Error.Message, ar.Error.Type)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("groq: status %d", resp.StatusCode)
	}
	out := &llm.CompletionResponse{
		ID: ar.ID, Model: ar.Model,
		Usage: llm.Usage{
			PromptTokens:     ar.Usage.PromptTokens,
			CompletionTokens: ar.Usage.CompletionTokens,
			TotalTokens:      ar.Usage.TotalTokens,
		},
	}
	for _, c := range ar.Choices {
		out.Choices = append(out.Choices, llm.Choice{
			Index:        c.Index,
			FinishReason: c.FinishReason,
			Message:      llm.Message{Role: c.Message.Role, Content: c.Message.Content},
		})
	}
	return out, nil
}
