package importer

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/livechat/hubspot-customer-qualification-example/internal/hubspot"
	"github.com/livechat/hubspot-customer-qualification-example/internal/textapi"
)

type source struct {
	pages []hubspot.Page
	index int
}

func (s *source) List(context.Context, string) (hubspot.Page, error) {
	page := s.pages[s.index]
	s.index++

	return page, nil
}

type target struct {
	created []textapi.Input
}

func (t *target) Find(context.Context, []string) (map[string][]textapi.Customer, error) {
	return map[string][]textapi.Customer{
		"existing@example.com": {
			{
				ID: "already-present",
			},
		},
		"ambiguous@example.com": {
			{
				ID: "one",
			},
			{
				ID: "two",
			},
		},
	}, nil
}

func (t *target) Create(_ context.Context, inputs []textapi.Input) ([]textapi.Result, error) {
	t.created = append(t.created, inputs...)
	results := make([]textapi.Result, len(inputs))

	for i := range results {
		results[i].ID = "created"
	}

	return results, nil
}

func TestDuplicatesAcrossPagesAndInvalidDataAreSkipped(t *testing.T) {
	contact := func(email string) hubspot.Contact {
		return hubspot.Contact{
			Properties: map[string]string{
				"email":     email,
				"firstname": "Synthetic",
			},
		}
	}
	first := hubspot.Page{
		Results: []hubspot.Contact{
			contact("duplicate@example.com"),
			contact(""),
			contact("Display Name <email@example.com>"),
			contact("existing@example.com"),
		},
	}
	first.Paging.Next.After = "next"
	second := hubspot.Page{
		Results: []hubspot.Contact{
			contact(" DUPLICATE@EXAMPLE.COM "),
			contact("new@example.com"),
			contact("ambiguous@example.com"),
		},
	}
	s := &source{
		pages: []hubspot.Page{first, second},
	}
	dst := &target{}

	counts, err := Run(context.Background(), s, dst, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	if counts.Duplicate != 2 || counts.MissingEmail != 1 || counts.Invalid != 1 || counts.Existing != 1 ||
		counts.Ambiguous != 1 ||
		counts.Created != 1 ||
		len(dst.created) != 1 ||
		dst.created[0].Email != "new@example.com" {
		t.Fatalf("counts=%+v creates=%+v", counts, dst.created)
	}
}
