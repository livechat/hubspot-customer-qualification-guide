# HubSpot contacts and Text lead qualification

Imports your HubSpot contacts into Text as customers, then updates a `text_qualified_lead` checkbox on a HubSpot contact when Text qualifies or disqualifies that customer while they're online.

See the [full setup guide](https://www.text.com/docs/guides/sync-hubspot-qualified-leads).

## Quickstart

Requires Docker with Docker Compose, a Text personal access token with the `customers:ro` and `customers:rw` scopes, and a HubSpot Service Key (or an existing private app token) with the `crm.objects.contacts.read`, `crm.objects.contacts.write`, and `crm.schemas.contacts.read` scopes.

Follow the setup guide above to define qualification in Text, create the `text_qualified_lead` contact property in HubSpot, and create both credentials. Create the Text token on an agent account rather than a bot account — that agent's team membership decides which customers the listener sees.

```shell
git clone https://github.com/livechat/hubspot-customer-qualification-guide.git
cd hubspot-customer-qualification-guide
cp .env.example .env    # set TEXT_TOKEN_BASE64 and HUBSPOT_ACCESS_TOKEN
```

Import the contacts. Start with a dry run, which reports what a real import would do without writing anything to Text:

```shell
docker compose run --rm --build qualification import --dry-run
docker compose run --rm qualification import
```

Each run prints a counter summary; the setup guide explains every counter. Run one import at a time, and check Text before rerunning an import that stopped partway, because there's no rollback.

Then start the qualification listener:

```shell
docker compose up -d --build qualification
curl --fail --include http://localhost:8080/readyz    # use HOST_PORT if you changed it
```

`/readyz` returns HTTP `204` once the service is connected to Text and `503` while it's disconnected. Follow the logs with `docker compose logs --follow qualification`, and stop the service with `docker compose down`.

**The listener covers online customers only, and only while it's running.** Text monitors customers who are online in the agent's teams, so an imported contact who never visits your site never produces an update. A change that happens while the listener is stopped is lost too — Text doesn't queue it, and the listener doesn't backfill on reconnect.

## Configuration

| Variable               | Required | Default                                          | Description                                                                                          |
| ---------------------- | -------- | ------------------------------------------------ | ---------------------------------------------------------------------------------------------------- |
| `TEXT_TOKEN_BASE64`    | Yes      | —                                                | Base64-encoded `account_id:personal_access_token` credential from Text, without the `Basic ` prefix. |
| `HUBSPOT_ACCESS_TOKEN` | Yes      | —                                                | HubSpot Service Key or private app access token.                                                     |
| `HOST_PORT`            | No       | `8080`                                           | Local port the health endpoints bind to on your machine.                                             |
| `TEXT_CDP_URL`         | No       | `https://api.text.com/cdp`                       | Customer Data Platform API base URL.                                                                 |
| `TEXT_ACCOUNTS_URL`    | No       | `https://accounts.text.com`                      | Accounts API base URL.                                                                               |
| `TEXT_RTM_URL`         | No       | `wss://api.livechatinc.com/v3.6/agent/rtm/ws`    | Agent Chat RTM API URL.                                                                              |
| `HUBSPOT_URL`          | No       | `https://api.hubapi.com`                         | HubSpot API base URL.                                                                                |
| `HTTP_MIN_INTERVAL`    | No       | `200ms`                                          | Minimum delay between outgoing API requests.                                                         |

Keep `.env` private because it holds both credentials. Git and the Docker build ignore it.

The URL variables exist so the test suite can point the service at a local mock server. Leave them unset to run against the live API.

## Development

Requires Go 1.27. The only dependency is `github.com/coder/websocket`. Running the binary directly with `go run` reads the environment, so export the variables yourself; it doesn't load `.env`. The binary binds the port in `PORT`, while `HOST_PORT` is only Compose's host-side mapping.

```shell
go test ./...
```

CI also runs `go vet ./...` and checks formatting with the pinned style tools:

```shell
go install mvdan.cc/gofumpt@v0.11.0
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.1

gofumpt -w cmd internal
golangci-lint run --fix ./...
```

## License

Licensed under the [MIT License](LICENCE.md). [Third-party notices](THIRD_PARTY_NOTICES.md) cover its dependency.

---

See the full [Text documentation reference](https://www.text.com/docs).
