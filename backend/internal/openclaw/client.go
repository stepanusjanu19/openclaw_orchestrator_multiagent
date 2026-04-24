// internal/openclaw/client.go
// HTTP client ke OpenClaw Gateway.
// Credential dibaca dari config, tidak hardcode.

package openclaw

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client adalah wrapper untuk OpenClaw Gateway HTTP API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient membuat client baru.
func NewClient(gatewayURL, token string) *Client {
	return &Client{
		baseURL: gatewayURL,
		token:   token,
		http: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// ChatRequest adalah request body ke OpenClaw gateway.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	User     string    `json:"user,omitempty"`
}

// Message adalah satu chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatResponse adalah response dari OpenClaw gateway.
type ChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

// HealthCheck melakukan ping ke gateway.
func (c *Client) HealthCheck() error {
	// OpenClaw gateway tidak punya dedicated /health,
	// kita test dengan mengirim minimal request
	req, err := http.NewRequest("GET", c.baseURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gateway tidak bisa dihubungi: %w", err)
	}
	defer resp.Body.Close()

	// 404 or 405 still means gateway is alive
	if resp.StatusCode >= 500 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gateway error %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Chat mengirim pesan ke agent tertentu dan mengembalikan reply.
func (c *Client) Chat(model, userText, sessionKey string) (string, error) {
	reqBody := ChatRequest{
		Model: model,
		Messages: []Message{
			{Role: "user", Content: userText},
		},
		User: sessionKey,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("gagal marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("request gagal: %w", err)
	}
	defer resp.Body.Close()

	if !isSuccess(resp.StatusCode) {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gateway %d: %s", resp.StatusCode, string(body))
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("gagal parse response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("response kosong dari gateway")
	}

	return chatResp.Choices[0].Message.Content, nil
}

func isSuccess(code int) bool {
	return code >= 200 && code < 300
}
