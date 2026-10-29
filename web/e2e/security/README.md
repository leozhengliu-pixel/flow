# Security Browser Checks

Run `npm run test:e2e:security` against the production web build and Go API.
Each API uses a disposable database. The default run needs no provider access:
players and server-fetched cards use local fixtures. Set
`FLOW_SECURITY_API_BIN`, `FLOW_SECURITY_FIXTURE_API_BIN` and
`FLOW_SECURITY_WEB_DIST` to reuse builds. The fixture API is built with
`go build -tags securitye2e -o <temporary-path> ./cmd/server` from `api/`.

## Server-Fetched Cards

`link-cards.spec.ts` pastes X, Figma, GitHub and GitLab links into a document.
The API parses real oEmbed, Open Graph and raw file responses from a local
fixture server; Playwright does not mock the preview API responses. Assertions
cover rendered text, line ranges, the preview image, safe link attributes,
unexecuted provider markup, no provider frames and rejected URLs/redirects.

Production's outbound policy rejects loopback even when private endpoints are
trusted. The `securitye2e` build alone replaces the three preview clients with
a transport that rewrites only an explicit provider-host allow-list to one
HTTP loopback fixture origin. Unlisted hosts cannot reach the fixture or the
network. URL parsers, credential selection and redirect restrictions are still
real; the production DNS/dial policy is covered by Go outbound tests, not by
this transport. Never deploy the fixture binary.

## Real Providers

Set `FLOW_SECURITY_LIVE_EMBEDS=1` to enable `live-embeds.spec.ts`. YouTube has a
public default video; set the following to public, embeddable share links for
the other providers (missing links are skipped):

- `FLOW_SECURITY_LIVE_YOUTUBE_URL` (optional override)
- `FLOW_SECURITY_LIVE_LOOM_URL`
- `FLOW_SECURITY_LIVE_DESCRIPT_URL`
- `FLOW_SECURITY_LIVE_TELLA_URL`
- `FLOW_SECURITY_LIVE_MIRO_URL`

These checks make real browser requests without interception and verify a
successful frame response under enforced CSP. A failed network preflight
skips the provider when offline; HTTP errors from a reachable provider fail.
Provider availability, consent screens and account restrictions are outside
the deterministic CI gate. Keep private links and credentials out of env vars.

## Service Worker

`service-worker.spec.ts` checks asset caching, a byte-changed worker activating
while the page remains open, removal of the old cache, the browser kill switch
(`localStorage['flow:disable-sw'] = '1'`) and a deployed unregistering script
being picked up by `registration.update()`. A local gateway serves versioned
worker scripts because browser request routing cannot intercept worker update
fetches. No checked-in worker or shared build output is edited.

## Hardware Passkeys

`passkeys.spec.ts` uses Chromium's virtual CTAP2 authenticator. Real security
keys, biometric prompts, OS credential pickers and cross-device QR flows
cannot be automated by this suite. Manually verify on a production-like HTTPS
origin with enforced headers:

1. Open `/<workspace>/settings/account/security`, click **New passkey**, and
   complete the OS or hardware-key ceremony. Confirm the passkey is listed.
2. Require two-factor authentication for the workspace, sign in in a fresh
   browser session, click **Verify with passkey**, and complete the ceremony.
   Confirm that the workspace opens.
3. Repeat with a roaming security key and a platform authenticator. Cancel
   a ceremony and retry; check that cancellation neither verifies the session
   nor leaves a half-registered passkey.

## CI Follow-Ups

`go-test-timings.yml` opens or updates a weekly pull request using only
`GITHUB_TOKEN`. Enable **Allow GitHub Actions to create and approve pull
requests** in repository Actions settings. Token-created pull requests do not
automatically trigger CI; a maintainer can dispatch CI on that branch before
merging. The refresh takes median durations from the latest three successful
main push runs, and the CI shard checks verify exact coverage after rebalancing.

Failed main runs `40817994408` and `40814461733` both timed out in
`config.webServer` after 120 seconds, before any test ran. Their uploaded
`e2e-failure` artifacts contained empty API logs and no test traces. The API's
cold `go run` compilation was inside the readiness deadline. CI now builds the
binary in its own step and passes `FLOW_E2E_API_BIN` to Playwright; security
checks reuse that binary too. This removes compilation from the readiness
budget without increasing timeouts or retries.
