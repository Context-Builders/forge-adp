// Package mistral implements llm.Provider for the Mistral AI API.
package mistral

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
	ModelMistralLarge = "mistral-large-latest"
	ModelMistralSmall = "mistral-small-latest"
	ModelMistralNemo  = "open-mistral-nemo"
	ModelMistral7b    = "open-mistral-7b"
	ModelMixtral8x7b  = "open-mixtral-8x7b"
	ModelMixtral8x22b = "open-mixtral-8x22b"
	ModelCodestral    = "codestral-latest"
)

const defaultBaseURL = "https://api.mistral.ai"

// Config holds credentials for the Mistral provider.
type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider implements llm.Provider for Mistral AI.
type Provider struct {
	cfg    Config
	client *http.Client
}

// New returns a configured Mistral Provider.
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
func (p *Provider) Name() string { return "mistral" }

// Models returns the supported model identifiers.
func (p *Provider) Models() []string {
	return []string{
		ModelMistralLarge, ModelMistralSmall,
		ModelMistralNemo, ModelMistral7b,
		ModelMixtral8x7b, ModelMixtral8x22b,
		ModelCodestral,
	}
}

type mistralRequest struct {
	Model       string       `json:"model"`
	Messages    []mistralMsg `json:"messages"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Temperature float64      `json:"temperature,omitempty"`
	TopP        float64      `json:"top_p,omitempty"`
	Stop        []string     `json:"stop,omitempty"`
}

type mistralMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type mistralResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int        `json:"index"`
		Message      mistralMsg `json:"message"`
		FinishReason string     `json:"finish_reason"`
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

// Complete calls the Mistral Chat Completions endpoint.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	msgs := make([]mistralMsg, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = mistralMsg{Role: m.Role, Content: m.Content}
	}
	body, err := json.Marshal(mistralRequest{
		Model: req.Model, Messages: msgs,
		MaxTokens: req.MaxTokens, Temperature: req.Temperature,
		TopP: req.TopP, Stop: req.Stop,
	})
	if err != nil {
		return nil, fmt.Errorf("mistral: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mistral: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mistral: http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("mistral: read body: %w", err)
	}
	var ar mistralResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("mistral: decode: %w", err)
	}
	if ar.Error != nil {
		return nil, fmt.Errorf("mistral: %s (type=%s)", ar.Error.Message, ar.Error.Type)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mistral: status %d", resp.StatusCode)
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
