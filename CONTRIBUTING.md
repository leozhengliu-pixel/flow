# Contributing to Flow

Thank you for investing time in Flow. Contributions are welcome when they are
focused, testable, and consistent with the product's existing architecture.

By participating, you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Before You Start

- Search [existing issues](https://github.com/leozhengliu-pixel/flow/issues) before opening a new one.
- Use the issue forms for reproducible bugs and focused feature proposals.
- Discuss large UI changes, new dependencies, API contracts, or data migrations before implementation.
- Do not use public issues for security reports. Follow [SECURITY.md](SECURITY.md).

Small fixes may go directly to a pull request. Larger changes should have an
accepted issue that documents scope, user impact, and implementation direction.

## Development Setup

Fork the repository, then clone your fork:

```bash
git clone https://github.com/YOUR-USERNAME/flow.git
cd flow
git remote add upstream https://github.com/leozhengliu-pixel/flow.git
```

Start the API:

```bash
cd api
go run ./cmd/server
```

Start the web client in another terminal:

```bash
cd web
npm ci
npm run dev
```

Use a short-lived branch based on the latest `main`:

```bash
git fetch upstream
git switch -c feature/concise-description upstream/main
```

## Engineering Guidelines

### Scope

- Keep pull requests focused on one coherent change.
- Preserve existing module ownership and shared component patterns.
- Avoid unrelated formatting, dependency, or generated-file churn.
- Add abstractions only when they remove meaningful duplication or complexity.

### Web

- Use TypeScript and existing React component patterns.
- Reuse shared property menus, overlays, issue controls, and design tokens.
- Preserve keyboard navigation, focus management, responsive behavior, and both supported languages.
- Add translations for every new user-facing string.
- Do not commit `web/dist` or `web/node_modules`.

### API

- Keep workspace and team authorization at the request boundary.
- Persist related state and domain-event changes atomically.
- Validate inputs before mutating state or writing files.
- Add focused Go tests for handlers, permissions, persistence, and migrations.
- Do not commit databases, uploads, secrets, or development credentials.

## Required Checks

Run all checks before submitting a pull request:

```bash
cd web
npm ci
npm run lint
npm run test:coverage
npm run test:coverage:all
npm audit --audit-level=high
npm run build
npm run test:e2e

cd ../api
go vet ./...
go test -race -covermode=atomic -coverprofile=coverage.out ./...
../scripts/check-go-coverage.sh coverage.out

cd ..
git diff --check
```

CI runs the same frontend and backend checks on pull requests and pushes to
`main`.

## Commits

Write concise, imperative commit subjects. Conventional prefixes are encouraged:

```text
feat: add cycle capacity controls
fix: close property menu on outside click
docs: clarify reverse proxy configuration
test: cover guest issue permissions
```

Rebase or merge the latest `main` before review when necessary. Maintainers may
squash a pull request at merge time to keep history readable.

## Pull Requests

A pull request should:

- Explain the user problem and the chosen approach.
- Link the relevant issue when one exists.
- Describe API, schema, migration, or configuration changes.
- Include screenshots or recordings for visible UI changes.
- Include tests appropriate to the risk and scope.
- Update documentation and both languages when behavior or text changes.
- Pass CI with no new lint, type, or test failures.

Draft pull requests are welcome for early technical feedback, but they should be
marked ready only when the checklist is complete.

## Licensing

Unless you explicitly state otherwise, any contribution intentionally submitted
for inclusion in Flow is provided under the [Apache License 2.0](LICENSE), in
accordance with section 5 of that license. You must have the right to submit the
code, assets, documentation, and other material in your contribution.
