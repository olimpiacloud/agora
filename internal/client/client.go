package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	URL    string
	Token  string
	Cursor string
	HTTP   *http.Client
}

func FromEnv() *Client {
	u := os.Getenv("CARBON_URL")
	if u == "" {
		u = "http://127.0.0.1:7788"
	}
	tok := os.Getenv("CARBON_TOKEN")
	if tok == "" {
		for _, p := range []string{os.Getenv("CARBON_HOME"), "/var/lib/carbon", filepath.Join(os.Getenv("HOME"), ".carbon")} {
			if p == "" {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(p, "admin.token")); err == nil {
				tok = strings.TrimSpace(string(b))
				break
			}
		}
	}
	return &Client{URL: strings.TrimRight(u, "/"), Token: tok, Cursor: os.Getenv("CARBON_ACT_AS"), HTTP: &http.Client{Timeout: 11 * time.Minute}}
}

type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("carbon %d: %s", e.Status, e.Body) }

func (c *Client) Raw(ctx context.Context, method, path string, body any) ([]byte, string, error) {
	b, h, err := c.RawHeader(ctx, method, path, body)
	if err != nil {
		return nil, "", err
	}
	return b, h.Get("Content-Type"), nil
}

func (c *Client) RawHeader(ctx context.Context, method, path string, body any) ([]byte, http.Header, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, rd)
	if err != nil {
		return nil, nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.Cursor != "" {
		req.Header.Set("X-Carbon-Cursor", c.Cursor)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, nil, &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	return b, resp.Header, nil
}

func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	b, _, err := c.Raw(ctx, method, path, body)
	if err != nil {
		return err
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}
