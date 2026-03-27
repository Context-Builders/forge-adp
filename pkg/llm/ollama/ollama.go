// Package ollama implements llm.Provider for a locally-running Ollama server.
package ollama

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
	ModelLlama32_3b    = "llama3.2:3b"
	ModelLlama32_1b    = "llama3.2:1b"
	ModelLlama31_8b    = "llama3.1:8b"
	ModelLlama31_70b   = "llama3.1:70b"
	ModelMistral7b     = "mistral:7b"
	ModelMixtral8x7b   = "mixtral:8x7b"
	ModelCodeLlama7b   = "codellama:7b"
	ModelDeepSeekCoder = "deepseek-coder:6.7b"
	ModelGemma2_2b     = "gemma2:2b"
	ModelGemma2_9b     = "gemma2:9b"
	ModelQwen25_7b     = "qwen2.5:7b"
	ModelPhi3Mini      = "phi3:mini"
)

const defaultBaseURL = "http://localhost:11434"

// Config holds settings for the Ollama provider.
type Config struct {
	BaseURL    string
	Model      string
	KeepAlive  string
	HTTPClient *http.Client
}

// Provider implements llm.Provider for Ollama.
type Provider struct {
	cfg    Config
	client *http.Client
}

// New returns a configured Ollama Provider.
func New(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	cl := cfg.HTTPClient
	if cl == nil {
		cl = &http.Client{Timeout: 300 * time.Second}
	}
	return &Provider{cfg: cfg, client: cl}
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "ollama" }

// Models returns a set of common model identifiers available via Ollama.
func (p *Provider) Models() []string {
	return []string{
		ModelLlama32_3b, ModelLlama32_1b,
		ModelLlama31_8b, ModelLlama31_70b,
		ModelMistral7b, ModelMixtral8x7b,
		ModelCodeLlama7b, ModelDeepSeekCoder,
		ModelGemma2_2b, ModelGemma2_9b,
		ModelQwen25_7b, ModelPhi3Mini,
	}
}

type ollamaRequest struct {
	Model     string         `json:"model"`
	Messages  []ollamaMsg    `json:"messages"`
	Stream    bool           `json:"stream"`
	KeepAlive string         `json:"keep_alive,omitempty"`
	Options   *ollamaOptions `json:"options,omitempty"`
}

type ollamaMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	NumPredict  int      `json:"num_predict,omitempty"`
	Temperature float64  `json:"temperature,omitempty"`
	TopP        float64  `json:"top_p,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

type ollamaResponse struct {
	Model   string     `json:"model"`
	Message ollamaMsg  `json:"message"`
	DoneReason      string `json:"done_reason"`
	Done            bool   `json:"done"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error,omitempty"`
}

// Complete calls the Ollama /api/chat endpoint.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	model := req.Model
	if model == "" {
		model = p.cfg.Model
	}
	if model == "" {
		return nil, fmt.Errorf("ollama: no model specified")
	}

	msgs := make([]ollamaMsg, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = ollamaMsg{Role: m.Role, Content: m.Content}
	}

	apiReq := ollamaRequest{
		Model: model, Messages: msgs, Stream: false, KeepAlive: p.cfg.KeepAlive,
	}
	if req.MaxTokens > 0 || req.Temperature > 0 || req.TopP > 0 || len(req.Stop) > 0 {
		apiReq.Options = &ollamaOptions{
			NumPredict:  req.MaxTokens,
			Temperature: req.Temperature,
			TopP:        req.TopP,
			Stop:        req.Stop,
		}
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama: read body: %w", err)
	}
	var ar ollamaResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("ollama: decode: %w", err)
	}
	if ar.Error != "" {
		return nil, fmt.Errorf("ollama: %s", ar.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: status %d", resp.StatusCode)
	}
	finishReason := ar.DoneReason
	if finishReason == "" && ar.Done {
		finishReason = "stop"
	}
	return &llm.CompletionResponse{
		Model: ar.Model,
		Choices: []llm.Choice{{
			FinishReason: finishReason,
			Message:      llm.Message{Role: ar.Message.Role, Content: ar.Message.Content},
		}},
		Usage: llm.Usage{
			PromptTokens:     ar.PromptEvalCount,
			CompletionTokens: ar.EvalCount,
			TotalTokens:      ar.PromptEvalCount + ar.EvalCount,
		},
	}, nil
}
