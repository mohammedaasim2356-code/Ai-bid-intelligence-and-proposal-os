package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAICompatible calls any /v1/chat/completions endpoint (Ollama, llama.cpp, Groq,
// OpenRouter, Gemini's OpenAI-compatible endpoint, OpenAI, …). Keys stay server-side.
type OpenAICompatible struct {
	Client *http.Client
}

func (o OpenAICompatible) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return &http.Client{Timeout: 200 * time.Second}
}

// Complete sends a two-message chat completion and returns the assistant content.
func (o OpenAICompatible) Complete(ctx context.Context, p *ProviderConfig, system, user string, maxTokens int) (string, int, int, error) {
	jsonMode := p.JSONMode == nil || *p.JSONMode
	body := map[string]any{
		"model":       p.Model,
		"temperature": 0,
		"max_tokens":  maxTokens,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	if jsonMode {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	content, inTok, outTok, err := o.post(ctx, p, body)
	var he *HTTPError
	if err != nil && jsonMode && errors.As(err, &he) && he.Status == 400 {
		// endpoint does not support response_format: retry without it
		delete(body, "response_format")
		return o.post(ctx, p, body)
	}
	return content, inTok, outTok, err
}

func (o OpenAICompatible) post(ctx context.Context, p *ProviderConfig, body map[string]any) (string, int, int, error) {
	b, _ := json.Marshal(body)
	url := strings.TrimRight(p.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return "", 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := o.client().Do(req)
	if err != nil {
		return "", 0, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return "", 0, 0, &HTTPError{Status: resp.StatusCode, Body: string(raw)}
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", 0, 0, fmt.Errorf("provider returned non-JSON body: %s", truncate(string(raw), 120))
	}
	if out.Error != nil {
		return "", 0, 0, errors.New(out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", 0, 0, errors.New("provider returned no choices")
	}
	return out.Choices[0].Message.Content, out.Usage.Prompt, out.Usage.Completion, nil
}

// Ping checks the /models endpoint of a provider (used by "Test connection").
func (o OpenAICompatible) Ping(ctx context.Context, p *ProviderConfig) error {
	url := strings.TrimRight(p.BaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &HTTPError{Status: resp.StatusCode, Body: string(raw)}
	}
	return nil
}
