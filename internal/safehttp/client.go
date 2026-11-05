// Package safehttp sends JSON requests without exposing credentials or customer data in errors.
package safehttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Payload describes the optional JSON request body and response destination.
type Payload struct {
	Input  any
	Output any // A pointer to the value to decode into.
}

type Error struct {
	Path   string
	Status int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: HTTP %d", e.Path, e.Status)
}

type Client struct {
	BaseURL       string
	Authorization string
	HTTP          *http.Client
	Interval      time.Duration
}

func New(base, auth string) *Client {
	return &Client{
		BaseURL:       base,
		Authorization: auth,
		Interval:      200 * time.Millisecond,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Do makes one request. The small delay keeps a large import from flooding the APIs.
func (c *Client) Do(ctx context.Context, method, path string, p Payload) error {
	if c.Interval > 0 {
		timer := time.NewTimer(c.Interval)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}

	endpoint := logPath(path)

	var payload []byte

	if p.Input != nil {
		var err error

		payload, err = json.Marshal(p.Input)
		if err != nil {
			return fmt.Errorf("%s: could not encode JSON request", endpoint)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%s: could not build request", endpoint)
	}

	req.Header.Set("Authorization", c.Authorization)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s: request failed", endpoint)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &Error{Path: endpoint, Status: res.StatusCode}
	}

	if p.Output != nil {
		if json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(p.Output) != nil {
			return fmt.Errorf("%s: could not decode JSON response", endpoint)
		}
	}

	return nil
}

func logPath(path string) string {
	path, _, _ = strings.Cut(path, "?")
	parts := strings.Split(path, "/")

	// HubSpot's last path segment can contain a primary email or a contact ID.
	if len(parts) == 6 && parts[1] == "crm" && parts[2] == "objects" && parts[4] == "contacts" {
		parts[5] = "{contact}"
	}

	return strings.Join(parts, "/")
}
