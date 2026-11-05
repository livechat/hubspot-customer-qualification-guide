package qualification

import (
	"context"

	"github.com/livechat/hubspot-customer-qualification-example/internal/textapi"
)

type Text interface {
	GetMerged(context.Context, string) (textapi.Customer, error)
	Find(context.Context, []string) (map[string][]textapi.Customer, error)
}

type HubSpot interface {
	SetQualification(context.Context, string, bool) (Outcome, error)
}

type Service struct {
	Text    Text
	HubSpot HubSpot
	Segment int
}

// A push triggers one attempt to copy the customer's current qualification.
func (s *Service) Sync(ctx context.Context, id string) (Outcome, error) {
	customer, err := s.Text.GetMerged(ctx, id)
	if err != nil {
		return NoOutcome, err
	}

	email, ok := customer.Email()
	if !ok {
		return MissingEmail, nil
	}

	qualified, known := customer.Qualification(s.Segment)
	if !known {
		return UnevaluatedSegment, nil
	}

	matches, err := s.Text.Find(ctx, []string{email})
	if err != nil {
		return NoOutcome, err
	}

	if len(matches[email]) != 1 || customer.ClusterID == "" ||
		matches[email][0].ClusterID != customer.ClusterID {
		return AmbiguousText, nil
	}

	return s.HubSpot.SetQualification(ctx, email, qualified)
}
