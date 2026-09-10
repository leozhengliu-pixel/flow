# Provider Adapters

Settings > Integrations opens the provider's setup and operations surface. Token-based providers become Connected only after a successful upstream verification. Tokens can be supplied through deployment environment variables or entered once into the encrypted credential store. An upstream refusal is displayed as an error rather than a successful connection.

| Provider | Implemented operations | Deployment credential |
| --- | --- | --- |
| Notion | Read page title and content; attach a page to an accessible issue | `FLOW_INTEGRATION_NOTION_ACCESS_TOKEN` |
| Figma | Read file metadata; attach a design file to an accessible issue | `FLOW_INTEGRATION_FIGMA_ACCESS_TOKEN` |
| Sentry | Preview an error; create a linked issue; import signed creation/trigger webhooks | `FLOW_INTEGRATION_SENTRY_ACCESS_TOKEN` |
| Intercom | Read conversations; create linked Asks/issues and eligible customer requests; import signed conversation webhooks | `FLOW_INTEGRATION_INTERCOM_ACCESS_TOKEN` |
| Google Calendar | Synchronize the authorizing member's current out-of-office interval; refresh every five minutes; show it in person details | `FLOW_INTEGRATION_GOOGLE_CALENDAR_ACCESS_TOKEN` |
| Cursor | Create v1 agents/runs from an issue and repository, read run status/result/PR, request cancellation | `FLOW_INTEGRATION_CURSOR_ACCESS_TOKEN` |
| Codex | Verify an app-server connection; start a thread/turn in a configured checkout, read status and request interruption | See below |
| Zapier | Idempotent issue creation, issue updates, event subscription and unsubscription through Flow webhooks | A Flow API key with appropriate scopes |

GitHub, GitLab and Slack retain their existing authorization, repository and webhook adapters. Native resource imports and MCP connectors are distinct integrations: adding a Notion MCP server grants Agent tool access; configuring the native Notion adapter enables page operations.

## API

`POST /api/integrations/{provider}/configure` accepts `token`, optional `name`, and optional `teamId` for automatic imports. Configuration requires a workspace administrator. The browser never receives the stored token. Provider API base URLs are deployment-owned; optional `FLOW_INTEGRATION_<PROVIDER>_API_URL` values cannot be redirected by a workspace-supplied configuration field.

`POST /api/integrations/{provider}/actions` accepts an `action` and the fields appropriate to it: `resourceId`, `issueId` (an issue ID or public identifier), `teamId`, `repository`, `branch`, `prompt`, `title`, `description`, or `url`. Responses contain a title, preview, public issue identifier, link or runtime status. Mutating operations using workspace-wide service credentials require an administrator, including when authenticated with an API key. Zapier issue actions instead use ordinary issue-write scopes and team visibility. Coding actions also honor workspace Agent access policies.

Native token credentials are stored with AES-GCM using `FLOW_CONNECTOR_SECRET_KEY`. Token refresh/reauthorization is required when upstream credentials expire or are revoked. Native tokens supplied manually are not assumed to have an OAuth refresh grant. Disconnecting removes stored credentials. No credentials enter workspace exports or ordinary event payloads.

## Incoming Events

Sentry and Intercom use `POST /api/integrations/{provider}/webhook?workspace=<workspace-key>`. Set the corresponding `FLOW_INTEGRATION_SENTRY_WEBHOOK_SECRET` or `FLOW_INTEGRATION_INTERCOM_WEBHOOK_SECRET`; unsigned or invalid requests are rejected. The signature is validated against the original body. Imports run with the active connection owner's workspace permissions and configured destination team.

Each imported source has a stable issue-creation key checked within the issue transaction. Replayed events do not duplicate issues, Asks or customer requests. Intercom customer association respects excluded/generic domains and support-data privacy; reduced-PII mode does not create identities from sender email. Freeform conversation bodies are retained as content and are not automatically anonymized.

Zapier actions use Flow API authentication. Creating/updating issues needs write access; managing event subscriptions needs admin scope. Provide a stable Zap run ID as `resourceId` when creating issues. Subscriptions use the existing signed Flow webhook delivery system and its bounded retries.

## Coding Environments

Cursor uses the v1 Cloud Agents API, including its separate agent and run IDs. Run status and PR URLs are fetched from the provider rather than inferred from a successful start request. Cancellation is marked requested until a later status query reports completion.

For Codex, configure `FLOW_CODEX_APP_SERVER_URL`, `FLOW_CODEX_APP_SERVER_TOKEN`, and `FLOW_CODEX_WORKSPACES`. The last variable is a JSON map from Flow workspace key to an allowed checkout path on the remote coding host. Clients cannot supply arbitrary working directories. The adapter performs initialize/initialized, thread/start and turn/start over the authenticated connection. It uses workspace-write sandboxing and does not approve requests for elevated permissions. Run records survive page reloads and are private to their initiating Flow user.

Codex app-server WebSocket transport is experimental in the official documentation. Deploy and authenticate the separate coding host deliberately; these adapters do not install provider applications or provision remote repository access for you.

## Validation Boundaries

Protocol tests use isolated HTTP/WebSocket providers and real Flow persistence: OAuth PKCE/refresh, encrypted credential ownership, SDK elicitation, signed webhooks, import idempotency and coding lifecycle. Live customer/provider-account authorization is not claimed without that deployment's credentials and permissions. Configuration examples contain no real secrets or demo workspace data.

References: [MCP authorization](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization), [MCP elicitation](https://modelcontextprotocol.io/specification/2025-11-25/client/elicitation), [Cursor Cloud Agents](https://cursor.com/docs/cloud-agent/api/endpoints), [Codex app-server](https://learn.chatgpt.com/docs/app-server).
