package qualification

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/livechat/hubspot-customer-qualification-example/internal/safehttp"
	"github.com/livechat/hubspot-customer-qualification-example/internal/textapi"
)

type textState struct {
	member bool
	reads  int
}

func (s *textState) GetMerged(context.Context, string) (textapi.Customer, error) {
	s.reads++
	c := textapi.Customer{
		ID:        "synthetic",
		ClusterID: "synthetic",
		Memberships: []textapi.Membership{
			{
				SegmentID: 1,
				Member:    s.member,
			},
		},
	}
	c.Monitoring = &struct {
		Email string `json:"email"`
	}{
		Email: "person@example.com",
	}

	return c, nil
}

func (s *textState) Find(context.Context, []string) (map[string][]textapi.Customer, error) {
	return map[string][]textapi.Customer{
		"person@example.com": {
			{
				ID:        "synthetic",
				ClusterID: "synthetic",
			},
		},
	}, nil
}

type sink struct {
	writes []bool
}

func (s *sink) SetQualification(_ context.Context, _ string, value bool) (Outcome, error) {
	s.writes = append(s.writes, value)

	return NoOutcome, &safehttp.Error{
		Path:   "/crm/objects/2026-09/contacts/{contact}",
		Status: 503,
	}
}

func TestFailedQualificationIsNotRetried(t *testing.T) {
	source := &textState{
		member: true,
	}
	target := &sink{}
	service := Service{
		Text:    source,
		HubSpot: target,
		Segment: 1,
	}

	outcome, err := service.Sync(context.Background(), "synthetic")
	if err == nil || outcome != NoOutcome || source.reads != 1 || len(target.writes) != 1 || !target.writes[0] {
		t.Fatalf("outcome=%s err=%v reads=%d writes=%v", outcome, err, source.reads, target.writes)
	}
}

type qualificationRecorder struct {
	writes []bool
}

func (r *qualificationRecorder) SetQualification(_ context.Context, _ string, value bool) (Outcome, error) {
	r.writes = append(r.writes, value)

	return Updated, nil
}

func TestSyncUsesMergedClusterIdentityAndCurrentQualification(t *testing.T) {
	for _, tc := range []struct {
		name      string
		eventID   string
		clusterID string
		matches   []string
		member    bool
		updated   bool
	}{
		{"standalone", "profile", "profile", []string{"profile"}, true, true},
		{"linked visitor qualifies", "browser", "profile", []string{"profile"}, true, true},
		{"linked visitor disqualifies", "browser", "profile", []string{"profile"}, false, true},
		{"unrelated profile", "browser", "profile", []string{"other"}, true, false},
		{"no email match", "browser", "profile", []string{}, true, false},
		{"multiple profiles", "browser", "profile", []string{"profile", "other"}, true, false},
		{"missing cluster", "browser", "", []string{"profile"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergedReads := 0

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body any

				switch r.URL.Path {
				case "/get_customer", "/get_merged_customer":
					merged := r.URL.Path == "/get_merged_customer"
					member := !tc.member

					if merged {
						mergedReads++
						member = tc.member
					}

					body = map[string]any{
						"customer_id": tc.eventID,
						"cluster_id":  tc.clusterID,
						"monitoring":  map[string]string{"email": "person@example.com"},
						"segment_memberships": []any{
							map[string]any{
								"segment_id": 1,
								"is_member":  member,
							},
						},
					}
				case "/v1/list_customers":
					customers := []any{}
					for _, id := range tc.matches {
						customers = append(customers, map[string]any{
							"customer_id": id,
							"cluster_id":  id,
							"monitoring":  map[string]string{"email": "person@example.com"},
						})
					}

					body = map[string]any{"customers": customers}
				default:
					t.Error("unexpected Text endpoint")
					w.WriteHeader(http.StatusNotFound)

					return
				}

				json.NewEncoder(w).Encode(body)
			}))
			defer srv.Close()

			client := &textapi.Client{
				API: safehttp.New(srv.URL, "Basic synthetic"),
			}
			client.API.Interval = 0
			target := &qualificationRecorder{}
			service := Service{
				Text:    client,
				HubSpot: target,
				Segment: 1,
			}

			outcome, err := service.Sync(context.Background(), tc.eventID)
			if tc.clusterID == "" {
				if err == nil || len(target.writes) != 0 {
					t.Fatal("missing cluster identity was accepted")
				}

				return
			}

			if err != nil || mergedReads != 1 {
				t.Fatalf("merged reads=%d err=%v", mergedReads, err)
			}

			if tc.updated {
				if outcome != Updated || len(target.writes) != 1 || target.writes[0] != tc.member {
					t.Fatalf("outcome=%s writes=%v", outcome, target.writes)
				}
			} else if outcome != AmbiguousText || len(target.writes) != 0 {
				t.Fatalf("outcome=%s writes=%v", outcome, target.writes)
			}
		})
	}
}
