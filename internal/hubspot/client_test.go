package hubspot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/livechat/hubspot-customer-qualification-guide/internal/qualification"
	"github.com/livechat/hubspot-customer-qualification-guide/internal/safehttp"
)

func TestPrimaryEmailAndUnchangedValueNeverWrite(t *testing.T) {
	for _, tc := range []struct {
		email string
		value string
		want  qualification.Outcome
	}{
		{"alias@example.com", "false", qualification.AmbiguousHubSpot},
		{"primary@example.com", "true", qualification.Unchanged},
	} {
		writes := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				writes++

				return
			}

			json.NewEncoder(w).Encode(map[string]any{
				"id": "synthetic",
				"properties": map[string]string{
					"email":  tc.email,
					Property: tc.value,
				},
			})
		}))
		c := &Client{
			API: safehttp.New(srv.URL, "Bearer synthetic"),
		}
		c.API.Interval = 0
		outcome, err := c.SetQualification(context.Background(), "primary@example.com", true)

		srv.Close()

		if err != nil || outcome != tc.want || writes != 0 {
			t.Fatalf("outcome=%s err=%v writes=%d", outcome, err, writes)
		}
	}
}

func TestCheckboxTypes(t *testing.T) {
	for _, kind := range []string{"enumeration", "bool"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"name":      Property,
				"type":      kind,
				"fieldType": "booleancheckbox",
				"options": []any{
					map[string]string{
						"value": "true",
					},
					map[string]string{
						"value": "false",
					},
				},
			})
		}))
		c := &Client{
			API: safehttp.New(srv.URL, "Bearer synthetic"),
		}
		c.API.Interval = 0
		err := c.ValidateProperty(context.Background())

		srv.Close()

		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
}
