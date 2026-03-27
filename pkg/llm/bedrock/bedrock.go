// Package bedrock implements llm.Provider for AWS Bedrock using the Converse API.
// Credentials are accepted in Config or resolved from standard AWS environment
// variables (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN, AWS_REGION).
package bedrock

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dotrage/forge-adp/pkg/llm"
)

const (
	// Anthropic Claude
	ModelClaude35Sonnet = "anthropic.claude-3-5-sonnet-20241022-v2:0"
	ModelClaude35Haiku  = "anthropic.claude-3-5-haiku-20241022-v1:0"
	ModelClaude3Sonnet  = "anthropic.claude-3-sonnet-20240229-v1:0"
	ModelClaude3Haiku   = "anthropic.claude-3-haiku-20240307-v1:0"
	ModelClaude3Opus    = "anthropic.claude-3-opus-20240229-v1:0"
	// Meta Llama
	ModelLlama31_8b   = "meta.llama3-1-8b-instruct-v1:0"
	ModelLlama31_70b  = "meta.llama3-1-70b-instruct-v1:0"
	ModelLlama31_405b = "meta.llama3-1-405b-instruct-v1:0"
	ModelLlama32_1b   = "meta.llama3-2-1b-instruct-v1:0"
	ModelLlama32_3b   = "meta.llama3-2-3b-instruct-v1:0"
	ModelLlama32_11b  = "meta.llama3-2-11b-instruct-v1:0"
	ModelLlama32_90b  = "meta.llama3-2-90b-instruct-v1:0"
	// Mistral on Bedrock
	ModelMistralLarge = "mistral.mistral-large-2402-v1:0"
	ModelMistralSmall = "mistral.mistral-small-2402-v1:0"
	// Amazon Titan
	ModelTitanTextLite    = "amazon.titan-text-lite-v1"
	ModelTitanTextExpress = "amazon.titan-text-express-v1"
	ModelTitanTextPremier = "amazon.titan-text-premier-v1:0"
	// Cohere
	ModelCohereCommandR     = "cohere.command-r-v1:0"
	ModelCohereCommandRPlus = "cohere.command-r-plus-v1:0"
	// AI21
	ModelJamba15Large = "ai21.jamba-1-5-large-v1:0"
	ModelJamba15Mini  = "ai21.jamba-1-5-mini-v1:0"
)

// Config holds AWS credentials and region for the Bedrock provider.
type Config struct {
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	HTTPClient      *http.Client
}

func (c *Config) resolve() {
	if c.Region == "" {
		c.Region = firstEnv("AWS_REGION", "AWS_DEFAULT_REGION")
	}
	if c.AccessKeyID == "" {
		c.AccessKeyID = os.Getenv("AWS_ACCESS_KEY_ID")
	}
	if c.SecretAccessKey == "" {
		c.SecretAccessKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
	}
	if c.SessionToken == "" {
		c.SessionToken = os.Getenv("AWS_SESSION_TOKEN")
	}
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// Provider implements llm.Provider for AWS Bedrock.
type Provider struct {
	cfg    Config
	client *http.Client
}

// New returns a configured Bedrock Provider.
func New(cfg Config) *Provider {
	cfg.resolve()
	cl := cfg.HTTPClient
	if cl == nil {
		cl = &http.Client{Timeout: 120 * time.Second}
	}
	return &Provider{cfg: cfg, client: cl}
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "bedrock" }

// Models returns the supported model identifiers.
func (p *Provider) Models() []string {
	return []string{
		ModelClaude35Sonnet, ModelClaude35Haiku,
		ModelClaude3Sonnet, ModelClaude3Haiku, ModelClaude3Opus,
		ModelLlama31_8b, ModelLlama31_70b, ModelLlama31_405b,
		ModelLlama32_1b, ModelLlama32_3b, ModelLlama32_11b, ModelLlama32_90b,
		ModelMistralLarge, ModelMistralSmall,
		ModelTitanTextLite, ModelTitanTextExpress, ModelTitanTextPremier,
		ModelCohereCommandR, ModelCohereCommandRPlus,
		ModelJamba15Large, ModelJamba15Mini,
	}
}

// --- Bedrock Converse API types -----------------------------------------

type converseRequest struct {
	Messages        []converseMsg    `json:"messages"`
	System          []systemBlock    `json:"system,omitempty"`
	InferenceConfig *inferenceConfig `json:"inferenceConfig,omitempty"`
}

type converseMsg struct {
	Role    string    `json:"role"`
	Content []textBlock `json:"content"`
}

type textBlock struct {
	Text string `json:"text"`
}

type systemBlock struct {
	Text string `json:"text"`
}

type inferenceConfig struct {
	MaxTokens     int      `json:"maxTokens,omitempty"`
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"topP,omitempty"`
	StopSequences []string `json:"stopSequences,omitempty"`
}

type converseResponse struct {
	Output struct {
		Message struct {
			Role    string      `json:"role"`
			Content []textBlock `json:"content"`
		} `json:"message"`
	} `json:"output"`
	StopReason string `json:"stopReason"`
	Usage      struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
		TotalTokens  int `json:"totalTokens"`
	} `json:"usage"`
	Message string `json:"message,omitempty"`
}

// Complete calls the AWS Bedrock Converse API.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	if p.cfg.Region == "" {
		return nil, fmt.Errorf("bedrock: AWS region is required")
	}
	if p.cfg.AccessKeyID == "" || p.cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("bedrock: AWS credentials are required")
	}

	var sysBlocks []systemBlock
	var msgs []converseMsg
	for _, m := range req.Messages {
		if m.Role == llm.RoleSystem {
			sysBlocks = append(sysBlocks, systemBlock{Text: m.Content})
		} else {
			msgs = append(msgs, converseMsg{
				Role:    m.Role,
				Content: []textBlock{{Text: m.Content}},
			})
		}
	}

	apiReq := converseRequest{Messages: msgs, System: sysBlocks}
	if req.MaxTokens > 0 || req.Temperature > 0 || req.TopP > 0 || len(req.Stop) > 0 {
		ic := &inferenceConfig{MaxTokens: req.MaxTokens, StopSequences: req.Stop}
		if req.Temperature > 0 {
			ic.Temperature = &req.Temperature
		}
		if req.TopP > 0 {
			ic.TopP = &req.TopP
		}
		apiReq.InferenceConfig = ic
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("bedrock: marshal: %w", err)
	}
	url := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse",
		p.cfg.Region, req.Model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bedrock: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if err := p.signRequest(httpReq, body); err != nil {
		return nil, fmt.Errorf("bedrock: sign: %w", err)
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("bedrock: http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bedrock: read body: %w", err)
	}
	var ar converseResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("bedrock: decode: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := ar.Message
		if msg == "" {
			msg = string(raw)
		}
		return nil, fmt.Errorf("bedrock: status %d: %s", resp.StatusCode, msg)
	}
	var text string
	for _, c := range ar.Output.Message.Content {
		text += c.Text
	}
	return &llm.CompletionResponse{
		Model: req.Model,
		Choices: []llm.Choice{{
			FinishReason: ar.StopReason,
			Message:      llm.Message{Role: ar.Output.Message.Role, Content: text},
		}},
		Usage: llm.Usage{
			PromptTokens:     ar.Usage.InputTokens,
			CompletionTokens: ar.Usage.OutputTokens,
			TotalTokens:      ar.Usage.TotalTokens,
		},
	}, nil
}

// --- AWS Signature Version 4 -------------------------------------------

func (p *Provider) signRequest(r *http.Request, body []byte) error {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	r.Header.Set("x-amz-date", amzDate)
	if p.cfg.SessionToken != "" {
		r.Header.Set("x-amz-security-token", p.cfg.SessionToken)
	}
	payloadHash := hexSHA256(body)
	signedHdrs, canonHdrs := buildCanonicalHeaders(r)
	canonURI := r.URL.EscapedPath()
	if canonURI == "" {
		canonURI = "/"
	}
	canonReq := strings.Join([]string{
		r.Method, canonURI, r.URL.Query().Encode(),
		canonHdrs, signedHdrs, payloadHash,
	}, "\n")
	svc := "bedrock-runtime"
	credScope := strings.Join([]string{dateStamp, p.cfg.Region, svc, "aws4_request"}, "/")
	strToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, credScope, hexSHA256([]byte(canonReq)),
	}, "\n")
	sigKey := deriveSigV4Key(p.cfg.SecretAccessKey, dateStamp, p.cfg.Region, svc)
	sig := hex.EncodeToString(hmacSHA256(sigKey, []byte(strToSign)))
	r.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		p.cfg.AccessKeyID, credScope, signedHdrs, sig,
	))
	return nil
}

func buildCanonicalHeaders(r *http.Request) (signedHeaders, canonHeaders string) {
	type kv struct{ k, v string }
	var pairs []kv
	seen := map[string]bool{}
	add := func(key, val string) {
		lk := strings.ToLower(key)
		if !seen[lk] {
			seen[lk] = true
			pairs = append(pairs, kv{lk, strings.TrimSpace(val)})
		}
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	add("host", host)
	for k, vs := range r.Header {
		lk := strings.ToLower(k)
		if lk == "host" || strings.HasPrefix(lk, "x-amz-") || lk == "content-type" {
			add(k, vs[0])
		}
	}
	for i := 1; i < len(pairs); i++ {
		for j := i; j > 0 && pairs[j].k < pairs[j-1].k; j-- {
			pairs[j], pairs[j-1] = pairs[j-1], pairs[j]
		}
	}
	names := make([]string, len(pairs))
	lines := make([]string, len(pairs))
	for i, p := range pairs {
		names[i] = p.k
		lines[i] = p.k + ":" + p.v
	}
	return strings.Join(names, ";"), strings.Join(lines, "\n") + "\n"
}

func deriveSigV4Key(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hexSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
