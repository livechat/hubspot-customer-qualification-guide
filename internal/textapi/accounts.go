package textapi

import (
	"context"
	"errors"
	"net/http"
	"uuid"

	"github.com/livechat/hubspot-customer-qualification-guide/internal/safehttp"
)

func OrganizationID(ctx context.Context, accounts *safehttp.Client) (string, error) {
	var info struct {
		OrganizationID uuid.UUID `json:"organization_id"`
	}

	if err := accounts.Do(ctx, http.MethodGet, "/v2/personal_token/info", safehttp.Payload{
		Output: &info,
	}); err != nil {
		return "", err
	}

	if info.OrganizationID == uuid.Nil() {
		return "", errors.New("Text token-info response has no organization UUID")
	}

	return info.OrganizationID.String(), nil
}
