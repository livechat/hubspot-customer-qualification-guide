# HubSpot contacts and Text qualification

A small Go example with two separate commands:

1. **Import HubSpot contacts into Text:** create missing customers using email and name, without updating existing Text profiles.
2. **Listen for Text qualification changes:** update an existing HubSpot contact's `text_qualified_lead` checkbox when a monitored Text customer enters or leaves the Qualified lead segment.

Both commands use one Docker image. There is no database, scheduler, public webhook endpoint, or reconciliation job.

## Setup

Requires Docker with Docker Compose, a HubSpot test account, and a Text test organization with an agent account and an already configured Qualified lead segment. The Text account must support online customer monitoring, and the agent must have access to the teams whose online customers you want to monitor. A bot account cannot run this RTM listener.

```sh
cp .env.example .env
```

Fill in the private `.env`:

| Variable | Description |
| --- | --- |
| `TEXT_TOKEN_BASE64` | The **Base64 encoded token** supplied by Text when creating a PAT. It encodes `account_id:personal_access_token`; omit the `Basic ` prefix. |
| `HUBSPOT_ACCESS_TOKEN` | HubSpot Service Key, or an existing private-app access token. |
| `HOST_PORT` | Local health endpoint port; defaults to `8080`. |

Create a Text PAT using the [personal access token guide](https://www.text.com/docs/authentication/personal-access-tokens). The import needs customer read/write access (`customers:ro`, `customers:rw`); the listener uses customer read access. The RTM login requests only customer push families and uses the same Basic credential as CDP. Live authentication and delivery must be verified for your test account; the [RTM login reference](https://www.text.com/docs/api/agent-chat/v3.6/rtm/status#login) currently illustrates Bearer credentials.

For new HubSpot credentials, follow the [Service Key setup](https://developers.hubspot.com/docs/apps/developer-platform/build-apps/authentication/account-service-keys). Grant `crm.objects.contacts.read`, `crm.objects.contacts.write`, and `crm.schemas.contacts.read` so the listener can validate its contact property. The application does not create properties or require schema write access.

The example defaults to production Text endpoints. When testing in another environment, set `TEXT_CDP_URL`, `TEXT_RTM_URL`, and `TEXT_ACCOUNTS_URL` in the private `.env` to endpoints from that same environment. Production tokens and test-environment tokens are not interchangeable.

Credentials stay in `.env`, which Git and the Docker build ignore. Native Go execution reads environment variables; it does not load `.env` automatically.

## Create the HubSpot property

Your HubSpot user needs permission to edit property settings. In **Settings → Properties**, select **Contact properties** and create:

| Setting | Value |
| --- | --- |
| Label | **Text qualified lead** |
| Internal name | **text_qualified_lead** |
| Field type | **Single checkbox** |

Choose a suitable contact property group. Check the internal name before saving; it cannot be changed afterwards. See [HubSpot's property instructions](https://knowledge.hubspot.com/properties/create-and-edit-properties).

The listener writes `true` on qualification and `false` on disqualification. It leaves lifecycle stage and other properties untouched. A newly created property can initially be unset; this example does not backfill existing qualification.

## Step 1: import contacts

Preview the complete import without writing to Text:

```sh
docker compose run --rm --build qualification import --dry-run
```

Then run the import:

```sh
docker compose run --rm qualification import
```

The command reads all non-archived HubSpot contacts, checks exact primary-email matches in Text, creates only missing customers in batches of up to 100, prints aggregate progress and elapsed time, and exits. It copies email and the combined first/last name only. Missing/invalid emails, invalid names, duplicate source emails, and ambiguous Text matches are skipped and counted. Email casing and surrounding whitespace are normalized; secondary emails and aliases are outside this example's scope.

There is no contact-count cap. The regular contacts list avoids HubSpot Search's 10,000-result limit. For 55,000 valid unique contacts that are all missing, expect approximately 550 HubSpot reads, 550 Text lookups, and 550 Text creates before extra matching pages. Measure runtime in your account; the local 55,000-contact synthetic test is not a live performance benchmark.

Only one import should run at a time. Contact data stays in memory, without exports or checkpoint files. Rerunning scans again and skips customers found in Text. It does not synchronize later profile edits or delete customers.

## Step 2: listen for qualification changes

```sh
docker compose up -d --build qualification
docker compose logs -f qualification
```

The service discovers the configured Qualified lead segment, validates the HubSpot checkbox, resolves your Text organization ID from the token-info endpoint, and logs in to Text RTM with online customer monitoring for the agent's teams. A segment push triggers a current-state lookup of the merged Text profile and a primary-email match in HubSpot. Text can keep separate browser and profile IDs for one customer; the listener verifies their shared `cluster_id` before syncing. Unrelated profiles sharing an email are still skipped. It patches the checkbox only when its value differs; it never creates a HubSpot contact.

```sh
curl --fail --include http://localhost:8080/readyz
docker compose down
```

`/readyz` returns `204` after RTM login succeeds and `503` while disconnected. `/healthz` reports process liveness. These endpoints bind to loopback on the host. Readiness describes the connection, not whether every qualification update succeeded.

For simplicity, this demo uses the monitoring subscription that Text creates automatically at login. It does not call `subscribe_customers` or configure group IDs or a customer limit. Text selects online customers from the agent's teams, subject to the account's monitoring allowance. This does not cover every imported contact.

To change which teams are covered, update the integration agent's team membership and [Text chat routing rules](https://www.text.com/help/chat-routing-rules/). More advanced integrations can add group-specific subscriptions through `subscribe_customers`; those subscriptions add to login monitoring and can produce duplicate pushes where their coverage overlaps. This walkthrough uses login monitoring only.

For the demo, keep the test visitor online in a team accessible to the integration agent, identify them using a primary email present in HubSpot, and change their data so they enter and then leave your existing Text Qualified lead segment. Observe the HubSpot checkbox change in both directions. Use a visitor that has one unambiguous Text email match.

**Event-only limitation:** RTM monitoring does not provide complete coverage for offline/import-only customers. Changes missed during downtime, failed updates, and visitors excluded by monitoring limits can leave HubSpot stale. Startup and reconnect restore listening without backfill. The listener sets its connection to away, but connecting with an agent account can affect that account's presence and monitoring settings; use a dedicated test agent for the walkthrough.

## Failures and privacy

Exit `0` means the import completed (or a dry run completed); exit `1` means an error. A partial import may already have created customers. There is no rollback. The final counters describe confirmed results and work checked before failure, not an atomic snapshot of both accounts.

Every HTTP request gets one attempt, including rate-limit and server-error responses. An import stops on failure. If a create request fails, inspect Text before rerunning: immediate read-after-write visibility and idempotency are not guaranteed by this example. Explicit item failures stop the run after counting the successful items in that response.

The listener reconnects with bounded backoff and exits after repeated short connection failures. A failed qualification update is logged once and the listener continues with later events. Its in-memory event buffer holds 256 pending updates; overflow discards an update and logs an error. There is no persistent queue or recovery of discarded events.

Logs contain counts, fixed outcome labels, sanitized endpoint paths, HTTP status codes, and elapsed time. They omit emails, names, customer IDs, credentials, payloads, raw API error bodies, and URLs containing email addresses. Keep `.env`, account exports, and live screenshots out of the public repository.

## Development

Requires Go 1.27. Docker uses Go 1.27.3. The only Go dependency is `github.com/coder/websocket`.

Install the pinned development tools for consistent formatting and blank lines:

```sh
go install mvdan.cc/gofumpt@v0.11.0
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.1
```

Format and check Go code with `gofumpt` and the `wsl_v5` linter configured in `.golangci.yml`:

```sh
gofumpt -w cmd internal
golangci-lint run --fix ./...
```

CI checks the same rules without changing files, then runs:

```sh
go test -race ./...
go vet ./...
```

See [technical notes](docs/technical-notes.md) for contracts and implementation boundaries, and [testing](docs/testing.md) for automated checks and the live acceptance checklist.

The [MIT license](LICENCE.md) applies to this example.
