package importer

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/livechat/hubspot-customer-qualification-example/internal/hubspot"
	"github.com/livechat/hubspot-customer-qualification-example/internal/identity"
	"github.com/livechat/hubspot-customer-qualification-example/internal/textapi"
)

type Source interface {
	List(context.Context, string) (hubspot.Page, error)
}

type Target interface {
	Find(context.Context, []string) (map[string][]textapi.Customer, error)
	Create(context.Context, []textapi.Input) ([]textapi.Result, error)
}

type Counts struct {
	Read         int
	MissingEmail int
	Invalid      int
	Duplicate    int
	Existing     int
	Ambiguous    int
	WouldCreate  int
	Created      int
	Failed       int
}

func (c Counts) Log(logger *slog.Logger, message string) {
	logger.Info(
		message,
		"read", c.Read,
		"missing_email", c.MissingEmail,
		"invalid", c.Invalid,
		"duplicate", c.Duplicate,
		"existing", c.Existing,
		"ambiguous", c.Ambiguous,
		"would_create", c.WouldCreate,
		"created", c.Created,
		"failed", c.Failed,
	)
}

// Collect source emails before creating anything so duplicate primary addresses
// on different HubSpot pages are all skipped. Data stays in memory, not on disk.
func Run(ctx context.Context, source Source, target Target, dry bool, logger *slog.Logger) (counts Counts, err error) {
	defer func() {
		counts.Log(logger, "import summary")
	}()

	inputs := []textapi.Input{}
	totals := map[string]int{}
	pages := map[string]bool{}

	for after := ""; ; {
		if pages[after] {
			return counts, errors.New("HubSpot pagination repeated a cursor")
		}

		pages[after] = true

		page, err := source.List(ctx, after)
		if err != nil {
			return counts, err
		}

		for _, contact := range page.Results {
			counts.Read++
			raw := contact.Properties["email"]

			if strings.TrimSpace(raw) == "" {
				counts.MissingEmail++
				continue
			}

			email, ok := identity.Normalize(raw)
			name := strings.TrimSpace(strings.TrimSpace(contact.Properties["firstname"]) + " " + strings.TrimSpace(contact.Properties["lastname"]))

			if !ok || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 255 {
				counts.Invalid++
				continue
			}

			totals[email]++
			inputs = append(inputs, textapi.Input{
				Email: email,
				Name:  name,
			})
		}

		if counts.Read%5000 == 0 && counts.Read != 0 {
			counts.Log(logger, "reading HubSpot contacts")
		}

		after = page.Paging.Next.After

		if after == "" {
			break
		}
	}

	unique := inputs[:0]

	for _, input := range inputs {
		if totals[input.Email] != 1 {
			counts.Duplicate++
			continue
		}

		unique = append(unique, input)
	}

	for offset := 0; offset < len(unique); offset += 100 {
		var (
			end    = min(offset+100, len(unique))
			batch  = unique[offset:end]
			emails = make([]string, len(batch))
		)

		for i, input := range batch {
			emails[i] = input.Email
		}

		found, err := target.Find(ctx, emails)
		if err != nil {
			return counts, err
		}

		missing := []textapi.Input{}

		for _, input := range batch {
			switch len(found[input.Email]) {
			case 0:
				missing = append(missing, input)
			case 1:
				counts.Existing++
			default:
				counts.Ambiguous++
			}
		}

		counts.WouldCreate += len(missing)

		if len(missing) > 0 && !dry {
			results, err := target.Create(ctx, missing)
			if err != nil {
				return counts, err
			}

			for _, result := range results {
				if result.Error != nil {
					counts.Failed++
				} else {
					counts.Created++
				}
			}

			if counts.Failed > 0 {
				return counts, errors.New("Text reported item failures; earlier successful creations remain in Text")
			}
		}

		counts.Log(logger, "import progress")
	}

	return counts, nil
}
