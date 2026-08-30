# Release automation

Release pipelines expose scoped access keys for CI systems. Generate a key from
the pipeline settings page, store it as an encrypted CI secret, and send release
events to the Flow API over HTTPS.

```bash
curl --fail-with-body \
  --request POST \
  --header "Authorization: Bearer ${FLOW_RELEASE_ACCESS_KEY}" \
  --header "Content-Type: application/json" \
  --data '{"version":"1.4.0","commitSha":"'"${GITHUB_SHA}"'","stage":"released"}' \
  "${FLOW_URL}/api/release-pipelines/${FLOW_PIPELINE_ID}/events"
```

Treat access keys like deployment credentials. Use one key per pipeline, rotate
it after accidental disclosure, and never print it in build logs. Restrict CI
network access to the Flow origin and verify TLS certificates.
