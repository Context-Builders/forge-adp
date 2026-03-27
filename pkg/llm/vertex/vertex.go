// Package vertex implements llm.Provider for Google Cloud Vertex AI and Gemini.
package vertex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dotrage/forge-adp/pkg/llm"
)

const (
	ModelGemini20Flash     = "gemini-2.0-flash-001"
	ModelGemini20FlashLite = "gemini-2.0-flash-lite-001"
	ModelGemini15Pro       = "gemini-1.5-pro"
	ModelGemini15Flash     = "gemini-1.5-flash"
	ModelGemini15Flash8b   = "gemini-1.5-flash-8b"
	ModelGemini10Pro       = "gemini-1.0-pro"
)

var vertexScopes = []string{"https://www.googleapis.com/auth/cloud-platform"}

const defaultLocation = "us-central1"

// Config holds project, location, and optional credential settings.
type Config struct {
	ProjectID       string
	Location        string
	CredentialsJSON []byte
	HTTPClient      *http.Client
}

// Provider implements llm.Provider for Google Vertex AI / Gemini.
type Provider struct {
	cfg    Config
	client *http.Client
}

// New returns a configured Vertex AI Provider.
func New(cfg Config) (*Provider, error) {
	if cfg.ProjectID == "" {
		cfg.ProjectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("vertex: ProjectID required (or set GOOGLE_CLOUD_PROJECT)")
	}
	if cfg.Location == "" {
		cfg.Location = defaultLocation
	}
	ctx := context.Background()
	var (
		ts  oauth2.TokenSource
		err error
	)
	if len(cfg.CredentialsJSON) > 0 {
		creds, cerr := google.CredentialsFromJSON(ctx, cfg.CredentialsJSON, vertexScopes...)
		if cerr != nil {
			return nil, fmt.Errorf("vertex: parse credentials: %w", cerr)
		}
		ts = creds.TokenSource
	} else {
		ts, err = google.DefaultTokenSource(ctx, vertexScopes...)
		if err != nil {
			return nil, fmt.Errorf("vertex: default credentials: %w", err)
		}
	}
	base := cfg.HTTPClient
	if base == nil {
		base = &http.Client{Timeout: 120 * time.Second}
	}
	return &Provider{cfg: cfg, client: &http.Client{
		Timeout: base.Timeout,
		Transport: &oauth2.Transport{Source: ts, Base: base.Transport},
	}}, nil
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "vertex" }

// Models returns the supported Gemini model identifiers.
func (p *Provider) Models() []string { return KnownModels() }

// KnownModels returns the supported Gemini model IDs without a Provider instance.
func KnownModels() []string {
	return []string{
		ModelGemini20Flash, ModelGemini20FlashLite,
		ModelGemini15Pro, ModelGemini15Flash, ModelGemini15Flash8b,
		ModelGemini10Pro,
	}
}

// --- Vertex AI generateContent types ------------------------------------

type genRequest struct {
	Contents          []vContent   `json:"contents"`
	SystemInstruction *vContent    `json:"systemInstruction,omitempty"`
	GenerationConfig  *genConfig   `json:"generationConfig,omitempty"`
}

type vContent struct {
	Role  string  `json:"role,omitempty"`
	Parts []vPart `json:"parts"`
}

type vPart struct {
	Text string `json:"text"`
}

type genConfig struct {
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	Temperature     float64  `json:"temperature,omitempty"`
	TopP            float64  `json:"topP,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type genResponse struct {
	Candidates []struct {
		Content struct {
			Role  string  `json:"role"`
			Parts []vPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Complete calls the Vertex AI generateContent endpoint for a Gemini model.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	var sysInst *vContent
	var contents []vContent

	for _, m := range req.Messages {
		switch m.Role {
		case llm.RoleSystem:
			if sysInst == nil {
				sysInst = &vContent{Parts: []vPart{{Text: m.Content}}}
			} else {
				sysInst.Parts = append(sysInst.Parts, vPart{Text: m.Content})
			}
		case llm.RoleAssistant:
			contents = append(contents, vContent{Role: "model", Parts: []vPart{{Text: m.Content}}})
		default:
			contents = append(contents, vContent{Role: "user", Parts: []vPart{{Text: m.Content}}})
		}
	}

	apiReq := genRequest{Contents: contents, SystemInstruction: sysInst}
	if req.MaxTokens > 0 || req.Temperature > 0 || req.TopP > 0 || len(req.Stop) > 0 {
		apiReq.GenerationConfig = &genConfig{
			MaxOutputTokens: req.MaxTokens,
			Temperature:     req.Temperature,
			TopP:            req.TopP,
			StopSequences:   req.Stop,
		}
	}
	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("vertex: marshal: %w", err)
	}
	url := fmt.Sprintf(
		"https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent",
		p.cfg.Location, p.cfg.ProjectID, p.cfg.Location, req.Model,
	)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("vertex: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("vertex: http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("vertex: read body: %w", err)
	}
	var ar genResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("vertex: decode: %w", err)
	}
	if ar.Error != nil {
		return nil, fmt.Errorf("vertex: %s (code=%d)", ar.Error.Message, ar.Error.Code)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vertex: status %d", resp.StatusCode)
	}
	out := &llm.CompletionResponse{
		Model: req.Model,
		Usage: llm.Usage{
			PromptTokens:     ar.UsageMetadata.PromptTokenCount,
			CompletionTokens: ar.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      ar.UsageMetadata.TotalTokenCount,
		},
	}
	for i, c := range ar.Candidates {
		var text string
		for _, pp := range c.Content.Parts {
			text += pp.Text
		}
		role := c.Content.Role
		if role == "model" {
			role = llm.RoleAssistant
		}
		out.Choices = append(out.Choices, llm.Choice{
			Index:        i,
			FinishReason: c.FinishReason,
			Message:      llm.Message{Role: role, Content: text},
		})
	}
	return out, nil
}
