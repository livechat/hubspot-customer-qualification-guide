package hubspot

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/livechat/hubspot-customer-qualification-guide/internal/identity"
	"github.com/livechat/hubspot-customer-qualification-guide/internal/qualification"
	"github.com/livechat/hubspot-customer-qualification-guide/internal/safehttp"
)

const Property = "text_qualified_lead"

const contactsPath = "/crm/objects/2026-09/contacts"

type Contact struct {
	ID         string            `json:"id"`
	Properties map[string]string `json:"properties"`
}

type Page struct {
	Results []Contact `json:"results"`
	Paging  struct {
		Next struct {
			After string `json:"after"`
		} `json:"next"`
	} `json:"paging"`
}

type Client struct {
	API *safehttp.Client
}

func (c *Client) List(ctx context.Context, after string) (Page, error) {
	q := url.Values{
		"limit":      {"100"},
		"archived":   {"false"},
		"properties": {"email,firstname,lastname"},
	}

	if after != "" {
		q.Set("after", after)
	}

	var page Page

	err := c.API.Do(ctx, http.MethodGet, contactsPath+"?"+q.Encode(), safehttp.Payload{
		Output: &page,
	})
	if err == nil && page.Results == nil {
		return page, errors.New("HubSpot returned an incomplete contact page")
	}

	return page, err
}

// SetQualification verifies the primary email before patching by contact ID.
// HubSpot can also resolve aliases; those are deliberately outside this example.
func (c *Client) SetQualification(ctx context.Context, email string, value bool) (qualification.Outcome, error) {
	q := url.Values{
		"idProperty": {"email"},
		"properties": {"email," + Property},
	}

	var contact Contact

	err := c.API.Do(
		ctx,
		http.MethodGet,
		contactsPath+"/"+url.PathEscape(email)+"?"+q.Encode(),
		safehttp.Payload{
			Output: &contact,
		},
	)

	var apiErr *safehttp.Error

	if errors.As(err, &apiErr) && apiErr.Status == 404 {
		return qualification.MissingHubSpot, nil
	}

	if err != nil {
		return qualification.NoOutcome, err
	}

	primary, ok := identity.Normalize(contact.Properties["email"])

	if !ok || primary != email || contact.ID == "" {
		return qualification.AmbiguousHubSpot, nil
	}

	wanted := strconv.FormatBool(value)

	if contact.Properties[Property] == wanted {
		return qualification.Unchanged, nil
	}

	err = c.API.Do(
		ctx,
		http.MethodPatch,
		contactsPath+"/"+url.PathEscape(contact.ID),
		safehttp.Payload{
			Input: map[string]any{
				"properties": map[string]string{
					Property: wanted,
				},
			},
		},
	)
	if err != nil {
		return qualification.NoOutcome, err
	}

	return qualification.Updated, nil
}

// ValidateProperty accepts both UI-created checkboxes and API-created booleans.
func (c *Client) ValidateProperty(ctx context.Context) error {
	var property struct {
		Name                 string
		Type                 string
		FieldType            string
		ModificationMetadata struct {
			ReadOnlyValue bool `json:"readOnlyValue"`
		} `json:"modificationMetadata"`
		Options []struct {
			Value string `json:"value"`
		} `json:"options"`
	}

	err := c.API.Do(
		ctx,
		http.MethodGet,
		"/crm/properties/2026-09/contacts/"+Property,
		safehttp.Payload{
			Output: &property,
		},
	)
	if err != nil {
		return err
	}

	valid := property.Type == "bool"

	if property.Type == "enumeration" {
		values := map[string]bool{}

		for _, o := range property.Options {
			values[o.Value] = true
		}

		valid = values["true"] && values["false"]
	}

	if property.Name != Property || property.FieldType != "booleancheckbox" || !valid ||
		property.ModificationMetadata.ReadOnlyValue {
		return errors.New("HubSpot text_qualified_lead must be a writable Single checkbox with true/false values")
	}

	return nil
}
