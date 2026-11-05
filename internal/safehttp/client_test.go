package safehttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPFailureIsNotRetriedAndUsesSanitizedPath(t *testing.T) {
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/crm/objects/2026-09/contacts/private%40example.com?token=secret", 429},
		{"/crm/objects/2026-09/contacts/123456789", 500},
	} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++

			http.Error(w, "private@example.com secret", tc.status)
		}))
		c := New(srv.URL, "Bearer synthetic")
		c.Interval = 0

		err := c.Do(context.Background(), http.MethodGet, tc.path, Payload{})

		srv.Close()

		var apiErr *Error

		if !errors.As(err, &apiErr) || apiErr.Status != tc.status || calls != 1 {
			t.Fatalf("err=%v requests=%d", err, calls)
		}

		if apiErr.Path != "/crm/objects/2026-09/contacts/{contact}" || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func TestRequestTimeoutIsNotRetried(t *testing.T) {
	calls := make(chan struct{}, 3)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls <- struct{}{}

		select {
		case <-r.Context().Done():
		case <-time.After(150 * time.Millisecond):
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "Basic synthetic")
	c.Interval = 0
	c.HTTP.Timeout = 25 * time.Millisecond

	err := c.Do(context.Background(), http.MethodPost, "/v1/create_customers", Payload{
		Input: map[string]string{"email": "private@example.com"},
	})
	if err == nil || len(calls) != 1 || strings.Contains(err.Error(), "private@example.com") {
		t.Fatalf("err=%v requests=%d", err, len(calls))
	}
}

func TestRedirectIsNotFollowedWithCredentials(t *testing.T) {
	destinationCalls := 0

	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls++ }))
	defer destination.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer srv.Close()

	c := New(srv.URL, "Bearer synthetic")
	c.Interval = 0

	if err := c.Do(context.Background(), http.MethodGet, "/read", Payload{}); err == nil || destinationCalls != 0 {
		t.Fatal("redirect followed or accepted")
	}
}
