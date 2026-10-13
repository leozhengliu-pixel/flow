# @flow/sdk

TypeScript SDK for the Flow REST API: a typed `FlowClient`, Relay-style
connections (`fetchNext()`, `for await`), typed errors with rate-limit details,
optional retries, file uploads, webhook verification and OAuth helpers. No
runtime dependencies; needs `fetch` (Node.js 18+, browsers, Bun, Deno).

```ts
import { FlowClient } from '@flow/sdk'

const client = new FlowClient({ apiKey: process.env.FLOW_API_KEY!, apiUrl: 'https://flow.example.com' })
const me = await client.viewer
for await (const issue of await client.issues({ first: 100 })) console.log(issue.identifier)
await client.createIssue({ teamId: 'team_1', title: 'Hello', description: '' })
```

```ts
import { FlowWebhookClient } from '@flow/sdk/webhooks'

const webhooks = new FlowWebhookClient(process.env.FLOW_WEBHOOK_SECRET!)
webhooks.on('Issue', event => console.log(event.action, event.data.identifier))
export const POST = webhooks.createFetchHandler() // or createNodeHandler() for Express
```

Documentation: [docs/sdk.md](../../docs/sdk.md). Example:
[docs/examples/sdk-quickstart.ts](../../docs/examples/sdk-quickstart.ts).

## Development

```bash
npm install
npm run build          # tsup: ESM + CJS + d.ts into dist/
npm test               # vitest, fetch is mocked
npx tsc --noEmit
node scripts/generate.mjs           # regenerate src/generated from ../../docs/openapi.json
node scripts/generate.mjs --check   # fails when src/generated is stale (CI)
```
