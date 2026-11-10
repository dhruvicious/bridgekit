package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL, token string
	http           *http.Client
}
type Repository struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
	HTMLURL  string `json:"html_url"`
}

func New(baseURL, token string) *Client {
	return &Client{strings.TrimRight(baseURL, "/"), token, &http.Client{Timeout: 20 * time.Second}}
}
func (c *Client) ListRepositories(ctx context.Context, owner string) ([]Repository, error) {
	path := "/user/repos"
	if owner != "" {
		path = "/users/" + url.PathEscape(owner) + "/repos"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path+"?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	var repos []Repository
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return nil, fmt.Errorf("decode GitHub response: %w", err)
	}
	return repos, nil
}
