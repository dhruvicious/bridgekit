package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL, token string
	http           *http.Client
}
type MessageResult struct {
	OK      bool   `json:"ok"`
	Channel string `json:"channel"`
	TS      string `json:"ts"`
	Error   string `json:"error,omitempty"`
}

func New(baseURL, token string) *Client {
	return &Client{strings.TrimRight(baseURL, "/"), token, &http.Client{Timeout: 20 * time.Second}}
}
func (c *Client) PostMessage(ctx context.Context, channel, text string) (MessageResult, error) {
	body, err := json.Marshal(map[string]string{"channel": channel, "text": text})
	if err != nil {
		return MessageResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return MessageResult{}, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return MessageResult{}, fmt.Errorf("Slack request: %w", err)
	}
	defer resp.Body.Close()
	var result MessageResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return MessageResult{}, fmt.Errorf("decode Slack response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || !result.OK {
		return MessageResult{}, fmt.Errorf("Slack API error (HTTP %d): %s", resp.StatusCode, result.Error)
	}
	return result, nil
}
