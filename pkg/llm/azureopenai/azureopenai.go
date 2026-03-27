// Package azureopenai implements llm.Provider for the Azure OpenAI Service.
package azureopenai

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
	ModelGPT4Turbo = "gpt-4-turbo"
	ModelO1Preview = "o1-preview"
	ModelO1Mini    = "o1-mini"
	ModelO3Mini    = "o3-mini"
)

const defaultAPIVersion = "2025-01-01-preview"

var reasoningModels = map[string]bool{
	ModelO1Preview: true, ModelO1Mini: true, ModelO3Mini: true,
}

// Config holds credentials and endpoint settings for the Azure OpenAI provider.
type Config struct {
	Endpoint       string
	APIKey         string
	DeploymentName string
	APIVersion     string
	HTTPClient     *http.Client
}

// Provider implements llm.Provider for Azure OpenAI.
type Provider struct {
	cfg    Config
	client *http.Client
}

// New returns a configured Azure OpenAI Provider.
func New(cfg Config) *Provider {
	if cfg.APIVersion == "" {
		cfg.APIVersion = defaultAPIVersion
	}
	cl := cfg.HTTPClient
	if cl == nil {
		cl = &http.Client{Timeout: 120 * time.Second}
	}
	return &Provider{cfg: cfg, client: cl}
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "azureopenai" }

// Models returns the supported model identifiers.
func (p *Provider) Models() []string {
	return []string{
		ModelGPT4o, ModelGPT4oMini, ModelGPT4Turbo,
		ModelO1Preview, ModelO1Mini, ModelO3Mini,
	}
}

type chatRequest struct {
	Messages    []chatMsg `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	TopP        *float64  `json:"top_p,omitempty"`
	Stop        []string  `json:"stop,omitempty"`
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
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Complete calls the Azure OpenAI Chat Completions endpoint.
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
	apiReq := chatRequest{Messages: msgs, Stop: req.Stop}
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
		return nil, fmt.Errorf("azureopenai: marshal: %w", err)
	}
	url := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		p.cfg.Endpoint, p.cfg.DeploymentName, p.cfg.APIVersion)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("azureopenai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-key", p.cfg.APIKey)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("azureopenai: http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("azureopenai: read body: %w", err)
	}
	var ar chatResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("azureopenai: decode: %w", err)
	}
	if ar.Error != nil {
		return nil, fmt.Errorf("azureopenai: %s (code=%s)", ar.Error.Message, ar.Error.Code)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("azureopenai: status %d", resp.StatusCode)
	}
	model := ar.Model
	if model == "" {
		model = p.cfg.DeploymentName
	}
	out := &llm.CompletionResponse{
		ID: ar.ID, Model: model,
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
