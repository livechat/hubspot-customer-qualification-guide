package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

const syntheticAuth = "synthetic-account:synthetic-personal-token"

type fakeAPIs struct {
	contacts   int
	existing   map[string]bool
	reads      int
	lookups    int
	creates    int
	failCreate bool
	partial    bool
}

func (f *fakeAPIs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	encode := func(value any) {
		_ = json.NewEncoder(w).Encode(value)
	}

	switch r.URL.Path {
	case "/crm/objects/2026-09/contacts":
		if r.Method != "GET" || r.URL.Query().Get("limit") != "100" {
			http.Error(w, "bad contract", 400)

			return
		}

		f.reads++
		after, _ := strconv.Atoi(r.URL.Query().Get("after"))
		rows := []any{}

		for i := after; i < min(after+100, f.contacts); i++ {
			rows = append(
				rows,
				map[string]any{
					"id": strconv.Itoa(i),
					"properties": map[string]string{
						"email":     fmt.Sprintf("person-%d@example.com", i),
						"firstname": "Synthetic",
						"lastname":  "Person",
					},
				},
			)
		}

		response := map[string]any{
			"results": rows,
		}

		if after+100 < f.contacts {
			response["paging"] = map[string]any{
				"next": map[string]string{
					"after": strconv.Itoa(after + 100),
					"link":  "https://unexpected.example/never-follow",
				},
			}
		}

		encode(response)
	case "/v1/list_customers":
		f.lookups++

		var input struct {
			Filters []struct {
				Operator string
				Clauses  []struct {
					Field    string
					Operator string
					Values   []string
				}
			}
			ResultsPerPage int `json:"results_per_page"`
		}

		if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Filters) != 1 ||
			input.Filters[0].Operator != "AND" ||
			len(input.Filters[0].Clauses) != 1 ||
			input.Filters[0].Clauses[0].Operator != "is" ||
			input.Filters[0].Clauses[0].Field != "email" {
			http.Error(w, "bad filter", 422)

			return
		}

		rows := []any{}

		for _, email := range input.Filters[0].Clauses[0].Values {
			if f.existing[email] {
				rows = append(rows, map[string]any{
					"customer_id": email,
					"monitoring": map[string]string{
						"email": email,
					},
				})
			}
		}

		encode(map[string]any{
			"customers": rows,
			"page_id":   "1",
			"total":     len(rows),
		})
	case "/v1/create_customers":
		f.creates++

		var input struct {
			Customers []struct {
				Email string
				Name  string
			}
		}

		if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Customers) == 0 || len(input.Customers) > 100 {
			http.Error(w, "bad batch", 422)

			return
		}

		if f.failCreate {
			http.Error(w, "private upstream body: do-not-log@example.com secret-token", 500)

			return
		}

		results := []any{}

		for i, c := range input.Customers {
			if f.partial && i == 1 {
				results = append(
					results,
					map[string]any{
						"error": map[string]string{
							"type":    "internal",
							"message": "private@example.com",
						},
					},
				)

				continue
			}

			if f.existing[c.Email] {
				http.Error(w, "unexpected duplicate creation", 409)

				return
			}

			f.existing[c.Email] = true
			results = append(results, map[string]string{
				"customer_id": c.Email,
			})
		}

		encode(map[string]any{
			"results": results,
		})
	default:
		http.Error(w, "Endpoint not modeled", 501)
	}
}

func configure(t *testing.T, base string) {
	t.Helper()
	t.Setenv("TEXT_TOKEN_BASE64", base64.StdEncoding.EncodeToString([]byte(syntheticAuth)))
	t.Setenv("HUBSPOT_ACCESS_TOKEN", "synthetic-hubspot-token")
	t.Setenv("TEXT_CDP_URL", base)
	t.Setenv("HUBSPOT_URL", base)
	t.Setenv("HTTP_MIN_INTERVAL", "0s")
}

func TestImport55000DryRunCreateAndRerun(t *testing.T) {
	f := &fakeAPIs{
		contacts: 55000,
		existing: map[string]bool{},
	}

	srv := httptest.NewServer(f)
	defer srv.Close()

	configure(t, srv.URL)

	var logs bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	if err := run(context.Background(), []string{"import", "--dry-run"}, logger); err != nil {
		t.Fatal(err)
	}

	if f.creates != 0 || len(f.existing) != 0 || f.reads != 550 || f.lookups != 550 {
		t.Fatalf("dry run: reads=%d lookups=%d creates=%d", f.reads, f.lookups, f.creates)
	}

	if !strings.Contains(logs.String(), `"would_create":55000`) {
		t.Fatal("missing preview count")
	}

	if err := run(context.Background(), []string{"import"}, logger); err != nil {
		t.Fatal(err)
	}

	if f.creates != 550 || len(f.existing) != 55000 {
		t.Fatalf("create calls=%d customers=%d", f.creates, len(f.existing))
	}

	if err := run(context.Background(), []string{"import"}, logger); err != nil {
		t.Fatal(err)
	}

	if f.creates != 550 {
		t.Fatal("rerun created existing customers")
	}

	if strings.Contains(logs.String(), "@example.com") ||
		strings.Contains(logs.String(), "synthetic-personal-token") {
		t.Fatal("private data in application logs")
	}
}

func TestCreateFailureIsNotRetriedAndIsRedacted(t *testing.T) {
	f := &fakeAPIs{
		contacts:   200,
		existing:   map[string]bool{},
		failCreate: true,
	}

	srv := httptest.NewServer(f)
	defer srv.Close()

	configure(t, srv.URL)

	var logs bytes.Buffer

	err := run(context.Background(), []string{"import"}, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err == nil || !strings.Contains(err.Error(), "/v1/create_customers: HTTP 500") || f.creates != 1 {
		t.Fatalf("err=%v creates=%d", err, f.creates)
	}

	if strings.Contains(err.Error()+logs.String(), "do-not-log") ||
		strings.Contains(err.Error()+logs.String(), "secret-token") {
		t.Fatal("private API body leaked")
	}
}

func TestPartialCreateStopsAndReportsSuccessfulItems(t *testing.T) {
	f := &fakeAPIs{
		contacts: 200,
		existing: map[string]bool{},
		partial:  true,
	}

	srv := httptest.NewServer(f)
	defer srv.Close()

	configure(t, srv.URL)

	var logs bytes.Buffer

	err := run(context.Background(), []string{"import"}, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err == nil || f.creates != 1 || len(f.existing) != 99 || !strings.Contains(logs.String(), `"created":99`) ||
		!strings.Contains(logs.String(), `"failed":1`) {
		t.Fatalf("err=%v creates=%d customers=%d logs=%s", err, f.creates, len(f.existing), logs.String())
	}
}

// roundTripFunc keeps the startup test entirely offline, including on wrong hosts.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestListenerUsesConfiguredAccountsEndpoint(t *testing.T) {
	configure(t, "https://api.example.test")
	t.Setenv("TEXT_ACCOUNTS_URL", "https://accounts.example.test")
	t.Setenv("PORT", "8080")

	original := http.DefaultTransport

	t.Cleanup(func() { http.DefaultTransport = original })

	accountsCalled := false
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := http.StatusOK
		body := ""

		switch {
		case r.URL.Host == "api.example.test" && r.URL.Path == "/crm/properties/2026-09/contacts/text_qualified_lead":
			body = `{"name":"text_qualified_lead","type":"bool","fieldType":"booleancheckbox"}`
		case r.URL.Host == "api.example.test" && r.URL.Path == "/v1/get_segment_definitions":
			body = `{"segment_definitions":[{"segment_id":1,"segment_type":"qualified_lead"}]}`
		case r.URL.Host == "accounts.example.test" && r.URL.Path == "/v2/personal_token/info":
			accountsCalled = true
			status = http.StatusUnauthorized
		default:
			t.Error("startup used an unexpected API endpoint")

			status = http.StatusBadGateway
		}

		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})

	err := run(context.Background(), []string{"listen"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !accountsCalled || err == nil || !strings.Contains(err.Error(), "/v2/personal_token/info: HTTP 401") {
		t.Fatalf("configured accounts called=%t err=%v", accountsCalled, err)
	}
}
