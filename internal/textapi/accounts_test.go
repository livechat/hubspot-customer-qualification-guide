package textapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"uuid"

	"github.com/livechat/hubspot-customer-qualification-guide/internal/safehttp"
)

func TestOrganizationIDLookupAuthenticatesAndNormalizesUUID(t *testing.T) {
	organization := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/personal_token/info" || r.Header.Get("Authorization") != "Basic synthetic" {
			t.Error("wrong token-info request")
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		json.NewEncoder(w).Encode(map[string]string{
			"organization_id": "urn:uuid:" + strings.ToUpper(organization.String()),
		})
	}))
	defer srv.Close()

	client := safehttp.New(srv.URL, "Basic synthetic")
	client.Interval = 0

	id, err := OrganizationID(context.Background(), client)
	if err != nil || id != organization.String() {
		t.Fatalf("organization=%s err=%v", id, err)
	}
}

func TestOrganizationIDRejectsMissingOrInvalidUUID(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"organization_id":"not-a-uuid"}`,
		`{"organization_id":"00000000-0000-0000-0000-000000000000"}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		}))
		client := safehttp.New(srv.URL, "Basic synthetic")
		client.Interval = 0
		id, err := OrganizationID(context.Background(), client)

		srv.Close()

		if err == nil || id != "" {
			t.Fatalf("accepted an invalid token-info response: id=%s err=%v", id, err)
		}
	}
}
