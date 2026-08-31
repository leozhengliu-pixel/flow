# Testing

Flow uses layered automated checks so contributors can validate behavior before
opening a pull request.

## Web

```bash
cd web
npm ci
npm run lint
npm run test:coverage
npm run test:coverage:workflows
npm run test:coverage:all
npm audit --audit-level=high
npm run build
npx playwright install chromium
npm run test:e2e
```

Vitest covers shared routing, label, filtering, cycle, pulse, resource-count,
localization, and design-system logic. Coverage thresholds are enforced in
`vitest.config.ts`. A second gate covers issue, project detail, settings, inbox,
picker, autosave, API transport, and collaboration workflows. `test:coverage:all` separately reports the complete source
tree with a regression floor, so the narrower high-confidence gate cannot be
mistaken for global UI coverage. Playwright starts
an isolated API and Vite server, creates a temporary workspace through the API,
and verifies desktop and mobile Chromium workflows without using repository seed
data.

## API

```bash
cd api
go vet ./...
go test -race -covermode=atomic -coverprofile=coverage.out ./...
../scripts/check-go-coverage.sh coverage.out
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

The 60% repository coverage floor prevents regressions while high-risk authentication,
authorization, OAuth, realtime, delivery, persistence, and upload paths should
remain above the repository average.

## Infrastructure

The CI infrastructure job runs build-tagged integration tests against real
PostgreSQL, MySQL, Redis, and MinIO containers. Run one locally by setting the
matching `FLOW_TEST_*` variables and using `go test -tags=integration` for the
target package.
