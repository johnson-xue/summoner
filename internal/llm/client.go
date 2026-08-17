package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client represents an LLM API client
type Client struct {
	config     ProviderConfig
	httpClient *http.Client
}

// NewClient creates a new LLM client with loaded configuration
func NewClient() (*Client, error) {
	config, err := LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	provider, err := config.GetProvider()
	if err != nil {
		return nil, fmt.Errorf("get provider: %w", err)
	}

	return &Client{
		config: provider,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}, nil
}

// NewClientFromConfig creates a client from specific provider config
func NewClientFromConfig(provider ProviderConfig) *Client {
	provider.setDefaults(provider.Name)
	return &Client{
		config: provider,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// ChatRequest represents a chat completion request
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// Message represents a chat message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse represents a chat completion response
type ChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// ExtractionResult represents the result of summary extraction
type ExtractionResult struct {
	Summary    string
	Score      int
	Fallback   bool // true when the score was defaulted (no/bad [SCORE:] line) — NOT a real LLM rating
	TokenUsage *TokenUsage
}

// TokenUsage represents token consumption
type TokenUsage struct {
	Prompt     int
	Completion int
	Total      int
}

// Extract extracts a summary from full output using LLM
func (c *Client) Extract(fullOutput, projectGuide string) (*ExtractionResult, error) {
	logDebug("Extracting summary from %d chars output", len(fullOutput))

	// Build extraction prompt
	systemPrompt := `你是一个摘要提取专家。从输出中提取 5-10 行关键信息。

要求：
1. 提取结论性信息（root cause, decisions, findings）
2. 保留关键细节（file paths, line numbers, function names）
3. 去除过程性信息（日志、调试步骤、中间尝试）
4. 如果有"下一步需要"或"依赖项"，一定包含

输出格式：
- 第一行：[SCORE: N] （1-5 分，5=信息完整）
- 后续行：关键信息摘要（每行一个要点，用 "- " 开头）`

	if projectGuide != "" {
		systemPrompt += fmt.Sprintf("\n\n项目特定指南：\n%s", projectGuide)
	}

	// Truncate long output to avoid token limits
	maxInputLen := 20000 // ~5k tokens
	if len(fullOutput) > maxInputLen {
		fullOutput = fullOutput[:maxInputLen] + "\n\n[... 输出过长，已截断 ...]"
	}

	// Prepare request
	reqBody := ChatRequest{
		Model: c.config.Model,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: fmt.Sprintf("请提取以下输出的关键信息：\n\n%s", fullOutput)},
		},
	}

	// Retry logic
	maxRetries := 3
	retryDelay := time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			logInfo("Retry attempt %d/%d after %v", attempt, maxRetries, retryDelay)
			time.Sleep(retryDelay)
			retryDelay *= 2 // Exponential backoff
		}

		result, err := c.doRequest(reqBody)
		if err != nil {
			// Check if retryable
			if attempt < maxRetries && isRetryable(err) {
				logWarn("Request failed: %v, retrying...", err)
				continue
			}
			return nil, fmt.Errorf("request failed after %d retries: %w", attempt, err)
		}

		// Success
		return result, nil
	}

	return nil, fmt.Errorf("unreachable")
}

func (c *Client) doRequest(reqBody ChatRequest) (*ExtractionResult, error) {
	// Build URL
	url := strings.TrimRight(c.config.BaseURL, "/") + c.config.ChatPath

	// SECURITY: Never log API keys, auth headers, or sensitive user data
	logDebug("Request: POST %s", url)
	logDebug("Model: %s", c.config.Model)

	// Marshal request
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")

	// Authentication
	apiKey := os.Getenv(c.config.APIKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("API key not found: please set %s environment variable", c.config.APIKeyEnv)
	}
	authValue := fmt.Sprintf("%s %s", c.config.AuthPrefix, apiKey)
	req.Header.Set(c.config.AuthHeader, authValue)

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &RetryableError{Err: err}
	}
	defer resp.Body.Close()

	logDebug("Response: HTTP %d", resp.StatusCode)

	// Check HTTP status
	if resp.StatusCode != http.StatusOK {
		// Limit error response body size to prevent OOM
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 10*1024)) // Max 10KB

		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, fmt.Errorf("authentication failed (401): invalid API key. Response: %s", string(bodyBytes))
		case http.StatusTooManyRequests:
			return nil, &RetryableError{Err: fmt.Errorf("rate limit exceeded (429): %s", string(bodyBytes))}
		case http.StatusBadRequest:
			return nil, fmt.Errorf("bad request (400): %s", string(bodyBytes))
		default:
			if resp.StatusCode >= 500 {
				return nil, &RetryableError{Err: fmt.Errorf("server error %d: %s", resp.StatusCode, string(bodyBytes))}
			}
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
		}
	}

	// Parse response with size limit
	var chatResp ChatResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1*1024*1024)) // Max 1MB
	if err := decoder.Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Check API error
	if chatResp.Error != nil {
		return nil, fmt.Errorf("API error: %s (%s)", chatResp.Error.Message, chatResp.Error.Type)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no response from LLM")
	}

	// Parse extraction result
	content := chatResp.Choices[0].Message.Content
	summary, score, fallback, err := parseExtractionResponse(content)
	if err != nil {
		return nil, err
	}

	result := &ExtractionResult{
		Summary:  summary,
		Score:    score,
		Fallback: fallback,
	}

	// Add token usage if available
	if chatResp.Usage != nil {
		result.TokenUsage = &TokenUsage{
			Prompt:     chatResp.Usage.PromptTokens,
			Completion: chatResp.Usage.CompletionTokens,
			Total:      chatResp.Usage.TotalTokens,
		}
		logInfo("Token usage: %d prompt + %d completion = %d total",
			result.TokenUsage.Prompt, result.TokenUsage.Completion, result.TokenUsage.Total)
	}

	return result, nil
}

// parseExtractionResponse splits the LLM's reply into a summary and a 1-5 score.
// The score is CLAMPED to [0,5]: an LLM that ignores the "1-5" instruction and
// returns [SCORE: 9] would otherwise make a downstream consumer
// (checkpoint.go: strings.Repeat("░", 5-score)) panic on a negative count. A
// fallback flag marks scores that were defaulted (no [SCORE:] line or unparseable)
// so the UI can distinguish "real 3/5" from "couldn't parse, assumed 3" (B6).
func parseExtractionResponse(content string) (summary string, score int, fallback bool, err error) {
	lines := strings.Split(content, "\n")

	// Extract score from first line
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "[SCORE:") {
		_, parseErr := fmt.Sscanf(lines[0], "[SCORE: %d]", &score)
		if parseErr == nil {
			summary = strings.Join(lines[1:], "\n")
		} else {
			// Score line present but unparseable (e.g. "[SCORE: abc]") — defaulted.
			score = 3
			fallback = true
			summary = content
		}
	} else {
		score = 3 // Default score
		fallback = true
		summary = content
	}

	// Clamp to valid range (A2): an out-of-range LLM score must never reach a
	// consumer that does slice/repeat arithmetic on (5 - score).
	if score < 0 {
		score = 0
		fallback = true
	} else if score > 5 {
		score = 5
		fallback = true
	}

	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "", 0, false, fmt.Errorf("empty summary")
	}

	return summary, score, fallback, nil
}

// Validate checks if the client is properly configured
func (c *Client) Validate() error {
	apiKey := os.Getenv(c.config.APIKeyEnv)
	if apiKey == "" {
		return fmt.Errorf("API key not found: please set %s environment variable", c.config.APIKeyEnv)
	}
	return nil
}

// GetInfo returns client configuration information
func (c *Client) GetInfo() string {
	return fmt.Sprintf(`Provider: %s
Base URL: %s
Model: %s
API Key Env: %s
Chat Path: %s`,
		c.config.Name, c.config.BaseURL, c.config.Model,
		c.config.APIKeyEnv, c.config.ChatPath)
}

// RetryableError wraps errors that should be retried
type RetryableError struct {
	Err error
}

func (e *RetryableError) Error() string {
	return e.Err.Error()
}

func (e *RetryableError) Unwrap() error {
	return e.Err
}

func isRetryable(err error) bool {
	var retryable *RetryableError
	return errors.As(err, &retryable)
}
