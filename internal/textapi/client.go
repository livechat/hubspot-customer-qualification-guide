package textapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/livechat/hubspot-customer-qualification-example/internal/identity"
	"github.com/livechat/hubspot-customer-qualification-example/internal/safehttp"
)

type Membership struct {
	SegmentID int  `json:"segment_id"`
	Member    bool `json:"is_member"`
}

func (m *Membership) UnmarshalJSON(data []byte) error {
	var value struct {
		SegmentID *int  `json:"segment_id"`
		Member    *bool `json:"is_member"`
	}

	if json.Unmarshal(data, &value) != nil || value.SegmentID == nil || value.Member == nil ||
		*value.SegmentID < 0 {
		return errors.New("invalid segment membership")
	}

	m.SegmentID = *value.SegmentID
	m.Member = *value.Member

	return nil
}

type Customer struct {
	ID         string `json:"customer_id"`
	ClusterID  string `json:"cluster_id"`
	Monitoring *struct {
		Email string `json:"email"`
	} `json:"monitoring"`
	Memberships []Membership `json:"segment_memberships"`
}

func (c Customer) Email() (string, bool) {
	if c.Monitoring == nil {
		return "", false
	}

	return identity.Normalize(c.Monitoring.Email)
}

func (c Customer) Qualification(segment int) (bool, bool) {
	for _, m := range c.Memberships {
		if m.SegmentID == segment {
			return m.Member, true
		}
	}

	return false, false
}

type Input struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type Result struct {
	ID    string `json:"customer_id"`
	Error *struct {
		Type string `json:"type"`
	} `json:"error"`
}

type Client struct {
	API *safehttp.Client
}

func (c *Client) Find(ctx context.Context, emails []string) (map[string][]Customer, error) {
	if len(emails) == 0 || len(emails) > 100 {
		return nil, errors.New("Text email lookup requires 1 to 100 emails")
	}

	requested := map[string]bool{}

	for _, email := range emails {
		requested[email] = true
	}

	found := map[string][]Customer{}
	seenPages := map[string]bool{}
	seenIDs := map[string]bool{}

	for page := "1"; page != ""; {
		if seenPages[page] {
			return nil, errors.New("Text pagination repeated a page")
		}

		seenPages[page] = true
		input := map[string]any{
			"filters": []any{
				map[string]any{
					"operator": "AND",
					"clauses": []any{
						map[string]any{
							"field":    "email",
							"operator": "is",
							"values":   emails,
						},
					},
				},
			},
			"results_per_page": 100,
			"page_id":          page,
		}

		var response struct {
			Customers []Customer `json:"customers"`
			Next      string     `json:"next_page_id"`
		}

		if err := c.API.Do(ctx, http.MethodPost, "/v1/list_customers", safehttp.Payload{
			Input:  input,
			Output: &response,
		}); err != nil {
			return nil, err
		}

		if response.Customers == nil {
			return nil, errors.New("Text returned an incomplete customer page")
		}

		for _, customer := range response.Customers {
			email, ok := customer.Email()

			// An unidentifiable result cannot safely be interpreted as an absent email.
			if !ok || !requested[email] || customer.ID == "" {
				return nil, errors.New("Text lookup returned an unverifiable email match; import stopped before creating this batch")
			}

			if !seenIDs[customer.ID] {
				found[email] = append(found[email], customer)
				seenIDs[customer.ID] = true
			}
		}

		page = response.Next
	}

	return found, nil
}

func (c *Client) Create(ctx context.Context, customers []Input) ([]Result, error) {
	var response struct {
		Results []Result `json:"results"`
	}

	if len(customers) == 0 || len(customers) > 100 {
		return nil, errors.New("Text create requires 1 to 100 customers")
	}

	if err := c.API.Do(
		ctx,
		http.MethodPost,
		"/v1/create_customers",
		safehttp.Payload{
			Input: map[string]any{
				"customers": customers,
			},
			Output: &response,
		},
	); err != nil {
		return nil, err
	}

	if len(response.Results) != len(customers) {
		return nil, errors.New("Text create returned an incomplete result; outcome unknown, do not replay the batch automatically")
	}

	for _, r := range response.Results {
		if (r.ID == "") == (r.Error == nil) {
			return nil, errors.New("Text create returned an invalid result; outcome unknown")
		}
	}

	return response.Results, nil
}

func (c *Client) GetMerged(ctx context.Context, id string) (Customer, error) {
	var customer Customer

	err := c.API.Do(
		ctx,
		http.MethodPost,
		"/get_merged_customer",
		safehttp.Payload{
			Input: map[string]string{
				"customer_id": id,
			},
			Output: &customer,
		},
	)
	if err == nil && (customer.ID != id || customer.ClusterID == "") {
		return customer, errors.New("Text returned an unverifiable merged customer identity")
	}

	return customer, err
}

func (c *Client) QualifiedSegment(ctx context.Context) (int, error) {
	var response struct {
		Definitions []struct {
			ID   int    `json:"segment_id"`
			Type string `json:"segment_type"`
		} `json:"segment_definitions"`
	}

	if err := c.API.Do(
		ctx,
		http.MethodPost,
		"/v1/get_segment_definitions",
		safehttp.Payload{
			Input:  map[string]any{},
			Output: &response,
		},
	); err != nil {
		return 0, err
	}

	id := -1

	for _, d := range response.Definitions {
		if d.Type == "qualified_lead" {
			if id != -1 {
				return 0, errors.New("Text has ambiguous qualified-lead segment definitions")
			}

			id = d.ID
		}
	}

	if id < 0 {
		return 0, fmt.Errorf("configure a Qualified lead segment in Text before starting the listener")
	}

	return id, nil
}
