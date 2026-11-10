package stripe

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
	baseURL, key string
	http         *http.Client
}
type Charge struct {
	ID       string `json:"id"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
	Created  int64  `json:"created"`
}
type ChargeList struct {
	Data    []Charge `json:"data"`
	HasMore bool     `json:"has_more"`
}

func New(baseURL, key string) *Client {
	return &Client{strings.TrimRight(baseURL, "/"), key, &http.Client{Timeout: 20 * time.Second}}
}
func (c *Client) ListCharges(ctx context.Context, limit int) (ChargeList, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	u := c.baseURL + "/v1/charges?" + url.Values{"limit": {fmt.Sprint(limit)}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ChargeList{}, err
	}
	req.SetBasicAuth(c.key, "")
	resp, err := c.http.Do(req)
	if err != nil {
		return ChargeList{}, fmt.Errorf("Stripe request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ChargeList{}, fmt.Errorf("Stripe returned HTTP %d", resp.StatusCode)
	}
	var result ChargeList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ChargeList{}, fmt.Errorf("decode Stripe response: %w", err)
	}
	return result, nil
}
