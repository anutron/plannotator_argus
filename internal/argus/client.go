// Package argus is a thin HTTP client for argus's plugin API. It supports
// the subset plannotator-argus needs: MCP tool registration and
// unregistration.
package argus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// PluginVersion is the X-Argus-Plugin-Version header value sent on every
// request. Argus does not enforce this today but plans to in a future
// major version.
const PluginVersion = "1"

// ErrUnauthorized is returned when argus replies 401 — typically a revoked
// or invalid scope token. Callers fail fast on this.
var ErrUnauthorized = errors.New("argus: unauthorized (check scope token)")

// Client is the HTTP client for argus's plugin API.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New returns a Client with sane HTTP timeouts for plugin use.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// ToolRegistration is the body posted to /api/mcp/tools.
type ToolRegistration struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	CallbackURL string         `json:"callback_url"`
	AuthHeader  string         `json:"auth_header"`
}

// RegisterTool POSTs (or upserts) a tool registration with argus. Argus
// responds 200 or 201 on success; either is treated as success.
func (c *Client) RegisterTool(ctx context.Context, reg ToolRegistration) error {
	body, err := json.Marshal(reg)
	if err != nil {
		return fmt.Errorf("marshal registration: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/mcp/tools", bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("post register tool %s: %w", reg.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("register tool %s: status %d: %s", reg.Name, resp.StatusCode, string(snippet))
	}
	return nil
}

// UnregisterTool DELETEs a tool registration. 200 and 404 are both treated
// as success (idempotent).
func (c *Client) UnregisterTool(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+"/api/mcp/tools/"+name, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("delete tool %s: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("unregister tool %s: status %d: %s", name, resp.StatusCode, string(snippet))
	}
	return nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-Argus-Plugin-Version", PluginVersion)
}
