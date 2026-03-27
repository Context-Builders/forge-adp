// Package openai implements llm.Provider for OpenAI GPT-4o, o1, and o3.
package openai

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
	ModelGPT4o     = "gpt-4o"
	ModelGPT4oMini = "gpt-4o-mini"
	ModelO1        = "o1"
	ModelO1Mini    = "o1-mini"
	ModelO1Preview = "o1-preview"
	ModelO3        = "o3"
	ModelO3Mini    = "o3-mini"
)

const defaultBaseURL = "https://api.openai.com"

// reasoningModels require special handling: no "system" role, no temperature.
var reasoningModels = map[string]bool{
	ModelO1: true, ModelO1Mini: true, ModelO1Preview: true,
	ModelO3: true, ModelO3Mini: true,
}

// Config holds credentials and optional overrides for the OpenAI provider.
type Config struct {
	APIKey     string
	OrgID      string
	BaseURL    string
	HTTPClient *http.Client
}

// Provider implements llm.Provider for OpenAI.
type Provider struct {
	cfg    Config
	client *http.Client
}

// New returns a configured OpenAI Provider.
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
func (p *Provider) Name() string { return "openai" }

// Models returns the supported model identifiers.
func (p *Provider) Models() []string {
	return []string{
		ModelGPT4o, ModelGPT4oMini,
		ModelO1, ModelO1Mini, ModelO1Preview,
		ModelO3, ModelO3Mini,
	}
}

type chatRequest struct {
	Model       string       `json:"model"`
	Messages    []chatMsg    `json:"messages"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
	TopP        *float64     `json:"top_p,omitempty"`
	Stop        []string     `json:"stop,omitempty"`
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int     `json:"index"`
		Message      chatMsg `json:"message"`
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

// Complete calls the OpenAI Chat Completions endpoint.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	isReasoning := reasoningModels[req.Model]
	msgs := make([]chatMsg, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := m.Role
		if isReasoning && role == llm.RoleSystem {
			role = llm.RoleUser
		}
		msgs = append(msgs, chatMsg{Role: role, Content: m.Content})
	}
	apiReq := chatRequest{Model: req.Model, Messages: msgs, Stop: req.Stop}
	if req.MaxTokens > 0 {
		apiReq.MaxTokens = req.MaxTokens
	}
	if !isReasoning {
		if req.Temperature > 0 {
			apiReq.Temperature = &req.Temperature
		}
		if req.TopP > 0 {
			apiReq.TopP = &req.TopP
		}
	}
	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	if p.cfg.OrgID != "" {
		httpReq.Header.Set("OpenAI-Organization", p.cfg.OrgID)
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: read body: %w", err)
	}
	var ar chatResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("openai: decode: %w", err)
	}
	if ar.Error != nil {
		return nil, fmt.Errorf("openai: %s (type=%s)", ar.Error.Message, ar.Error.Type)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: status %d", resp.StatusCode)
	}
	out := &llm.CompletionResponse{
		ID:    ar.ID,
		Model: ar.Model,
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
