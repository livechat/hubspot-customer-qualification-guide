package textapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/livechat/hubspot-customer-qualification-example/internal/safehttp"
)

func TestFindConsumesAllMatchingPages(t *testing.T) {
	calls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++

		var req struct {
			Page string `json:"page_id"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		if req.Page == "1" {
			w.Write([]byte(`{"customers":[{"customer_id":"one","monitoring":{"email":"one@example.com"}}],"next_page_id":"2"}`))
		} else {
			w.Write([]byte(`{"customers":[{"customer_id":"two","monitoring":{"email":"two@example.com"}}]}`))
		}
	}))
	defer srv.Close()

	c := &Client{
		API: safehttp.New(srv.URL, "Basic synthetic"),
	}
	c.API.Interval = 0

	found, err := c.Find(context.Background(), []string{"one@example.com", "two@example.com"})
	if err != nil {
		t.Fatal(err)
	}

	if len(found) != 2 || calls != 2 {
		t.Fatalf("found=%d calls=%d", len(found), calls)
	}
}

func TestUnverifiableMatchesAndRepeatedPagesFailClosed(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"customers":[{"customer_id":"one","monitoring":null}]}`,
		`{"customers":[],"next_page_id":"1"}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		}))
		c := &Client{
			API: safehttp.New(srv.URL, "Basic synthetic"),
		}
		c.API.Interval = 0
		_, err := c.Find(context.Background(), []string{"one@example.com"})

		srv.Close()

		if err == nil {
			t.Fatal("unsafe match or pagination accepted")
		}
	}
}
