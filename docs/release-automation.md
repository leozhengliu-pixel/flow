# Release automation

Release pipelines expose scoped access keys for CI systems. Generate a key from
the pipeline settings page (Settings › Releases › pipeline › CI setup), store it
as an encrypted CI secret named `FLOW_RELEASE_ACCESS_KEY`, and call the release
API over HTTPS. The key alone identifies the pipeline, so no pipeline id is needed.

The API offers the access-key operations used by release CLIs and
GitHub Actions. Every call sends `Authorization: Bearer $FLOW_RELEASE_ACCESS_KEY`.

| Endpoint | Purpose |
| --- | --- |
| `GET /api/release-ci/pipeline` | Pipeline settings: `type`, `stages`, `includePathPatterns` (the pipeline's path filters). |
| `GET /api/release-ci/releases?limit=20` | Recent releases, newest first (`id`, `name`, `version`, `commitSha`, `stage`, `url`). |
| `POST /api/release-ci/sync` | Create a release or add issues to the current one. |
| `POST /api/release-ci/update` | Move a release to a stage (scheduled pipelines). `stage` is required. |
| `POST /api/release-ci/complete` | Complete a release (scheduled pipelines). |

Request fields (all optional unless noted):

- `name`, `version`, `description` (an empty string clears it), `commitSha`,
  `preserveStoredCommitSha`.
- `issueReferences: [{ "identifier": "ENG-123", "commitSha": "…" }]` links issues;
  `revertedIssueReferences` unlinks them. Unknown identifiers are reported in
  `unknownIssueIdentifiers`.
- `links: [{ "url": "…", "label": "Pipeline" }]` (deduplicated by URL) and
  `documents: [{ "title": "Changelog", "content": "markdown" }]` (same title updates).
- `releaseNotes`: markdown string (or `{ "content": "…" }`).
- `stage` (required for update, optional for sync): matched exactly, then
  case-insensitively with `-`/`_` as spaces, then as a stage type (`planned`,
  `started`, `completed`, `canceled`). Sync moves the release only when `stage`
  is present; a stage type leaves a release that is already in a stage of that
  type where it is.

Targeting works like this:

- Sync without `version` uses the short commit SHA. Continuous pipelines create
  releases that are already completed. Scheduled pipelines add to the latest
  started release (or the latest planned one) and only create a release when none
  exists; new scheduled releases start in the first unfrozen started stage.
- Syncs never add issues to a release in a frozen stage; those identifiers are
  returned in `skippedIssueIdentifiers`.
- Update and complete without `version` target the latest started release
  (update falls back to the latest planned one).

Completing a release runs the pipeline's completion behaviour: team release
automations (production pipelines), "Auto-generate on completion" release notes
from the template (a `{{issues}}` line receives the completed issues), and, for
scheduled pipelines, "Move open issues to the next release" (open issues move to
the next planned or started release, which is created when none exists).

```bash
curl --fail-with-body -X POST "${FLOW_URL}/api/release-ci/sync" \
  -H "Authorization: Bearer ${FLOW_RELEASE_ACCESS_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"version":"1.4.0","commitSha":"'"${GITHUB_SHA}"'","issueReferences":[{"identifier":"ENG-123"}]}'
```

## GitHub Actions

Store the key as the `FLOW_RELEASE_ACCESS_KEY` repository secret, then add a
workflow that collects issue identifiers from the commits since the last synced
release (only the current commit on the first run) and syncs them. Replace
`ENG|WEB` with your team keys, and add `-- ':(glob)web/**'` pathspecs after
`$range` to honour path filters in a monorepo.

```yaml
name: Flow release
on:
  push:
    branches: [main]
jobs:
  flow-release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - name: Sync release to Flow
        env:
          FLOW_URL: https://flow.example.com
          FLOW_RELEASE_ACCESS_KEY: ${{ secrets.FLOW_RELEASE_ACCESS_KEY }}
        run: |
          auth="Authorization: Bearer $FLOW_RELEASE_ACCESS_KEY"
          last=$(curl -fsS -H "$auth" "$FLOW_URL/api/release-ci/releases?limit=1" | jq -r '.[0].commitSha // empty')
          if [ -n "$last" ]; then range="$last..HEAD"; else range="-1"; fi
          refs=$(git log --format=%B $range | { grep -oE '\b(ENG|WEB)-[0-9]+\b' || true; } | sort -u | jq -R '{identifier: .}' | jq -s .)
          jq -n --arg sha "$GITHUB_SHA" --argjson refs "$refs" '{commitSha: $sha, issueReferences: $refs}' |
            curl --fail-with-body -X POST "$FLOW_URL/api/release-ci/sync" -H "$auth" -H "Content-Type: application/json" --data @-
```

For scheduled pipelines, call `/api/release-ci/update` with a `stage` when a
release moves (for example on a release branch) and `/api/release-ci/complete`
when it ships.

The original per-pipeline endpoint `POST /api/release-pipelines/{id}/events`
(`version`, `name`, `description`, `commitSha`, `stage`) still works and accepts
the sync fields above.

## GitLab release webhooks

Projects without a GitLab Runner (for example when deployments run in a
separate PaaS CI/CD) can feed a release pipeline from their GitLab releases
instead of calling the CI API. Connect GitLab under Settings › Integrations,
then add a project (or group) webhook:

- URL: the connection's webhook URL plus the target pipeline, for example
  `https://flow.example.com/api/integrations/gitlab/webhook?workspace=acme&pipeline=mobile`.
  `pipeline` is the pipeline id or its slug. Without it the workspace's only
  release pipeline is used; with several pipelines the hook is answered with
  `202 {"ignored": true, "reason": "no release pipeline is selected: …"}` and
  nothing is written. An unknown `pipeline` answers 404.
- Secret token: the connection's webhook secret (sent as `X-Gitlab-Token`, as
  for merge request hooks).
- Trigger: **Releases events**. Tag push events are not releases (a tag can
  exist without a published release) and are ignored.

Each Release Hook (`object_kind: "release"`) is applied as a sync with
`stage: "completed"`, so issue linking, frozen stages, release history and the
completion behaviour above are the same as for `POST /api/release-ci/sync`:

| GitLab release | Flow release |
| --- | --- |
| `tag` | `version` |
| `name` (falls back to the tag) | name |
| `description` | description |
| `url` | a link resource labeled "GitLab release" |
| `KEY-123` identifiers in the name or description | linked issues (frozen stages skip them) |
| `commit.id` | `commitSha` |

- `create` and `update` upsert the release and move it to the pipeline's first
  unfrozen completed stage, which runs the completion automations. Releases of
  continuous pipelines are created completed; a release already in a completed
  stage stays where it is.
- Re-deliveries and updates of the same tag update the same release. A release
  is matched by version, then by version with a leading `v` ignored on either
  side (`v1.4.0` = `1.4.0`), then a release without a version by name
  (case-insensitive); that release gets the tag as its version.
- `delete` keeps the Flow release and answers
  `{"ignored": true, "reason": "GitLab release deleted; Flow releases are kept"}`.

## Access keys

Treat access keys like deployment credentials. The secret is shown once, when it
is generated or rotated. Rotating keeps the previous key working for one hour so
CI can be updated (or revokes it immediately when asked). Revoking stops the key
after one hour, or immediately. The settings page shows when the key was created
and last used. Never print the key in build logs; restrict CI network access to
the Flow origin and verify TLS certificates.
