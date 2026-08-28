# Security Policy

## Supported Versions

Flow is currently developed from a single release line.

| Version | Security updates |
| --- | --- |
| Latest `main` | Supported |
| Older commits and forks | Not supported |

Security fixes are applied to `main`. Until tagged releases are published, users
should update to the latest reviewed commit after testing it in their environment.

## Reporting a Vulnerability

Do not open a public issue for a suspected vulnerability.

Use GitHub's private
[Report a vulnerability](https://github.com/leozhengliu-pixel/flow/security/advisories/new)
form. Include:

- Affected commit or version.
- Affected API route, component, or configuration.
- Reproduction steps or a minimal proof of concept.
- Expected impact and required privileges.
- Any suggested mitigation, if available.

You should receive an acknowledgement within 5 business days. The maintainers
will validate the report, establish severity, coordinate a fix, and agree on a
disclosure timeline. Please allow a reasonable remediation period before public
disclosure.

## Security Expectations

- Never commit credentials, session tokens, databases, uploads, or private user data.
- Disable `FLOW_DEV_AUTH_TOKENS` outside local development.
- Create the first administrator account through the onboarding flow before inviting other users.
- Enable `FLOW_COOKIE_SECURE` behind HTTPS.
- Trust forwarded headers only from a controlled proxy that overwrites client-supplied values.
- Restrict filesystem permissions for the SQLite database and upload directory.
- Configure SMTP credentials through the runtime environment or a secret manager.
- Back up and test restoration of both the database and attachment storage.

## Scope

Reports about Flow's own code and default configuration are in scope. Vulnerabilities
in third-party services or dependencies should also be reported to their upstream
maintainers; include the impact on Flow when filing a private advisory here.
