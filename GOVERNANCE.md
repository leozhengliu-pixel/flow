# Governance

Flow uses a maintainer-led governance model focused on technical quality,
product coherence, and transparent decision-making.

## Roles

### Contributors

Contributors report issues, participate in design discussions, improve
documentation, review changes, and submit pull requests.

### Maintainers

Maintainers are trusted contributors responsible for:

- Triaging issues and pull requests.
- Protecting security, privacy, accessibility, and data integrity.
- Reviewing architecture, dependencies, migrations, and public API changes.
- Merging changes and managing releases.
- Enforcing the Code of Conduct and security policy.
- Keeping project documentation and automation healthy.

The current code owner is declared in [`.github/CODEOWNERS`](.github/CODEOWNERS).

## Decision Process

Routine changes are decided through pull request review. Significant changes
should begin with an issue that records the problem, constraints, alternatives,
and expected compatibility impact.

Maintainers aim for consensus. When consensus cannot be reached in a reasonable
time, the code owner makes the final decision and documents the reasoning in the
issue or pull request.

## Becoming a Maintainer

Maintainer access may be offered to contributors who consistently demonstrate:

- Sound technical judgment and constructive reviews.
- Respect for project scope and established architecture.
- Reliable participation over time.
- Care for users, contributors, security, and inclusive collaboration.

Access may be removed after prolonged inactivity, repeated policy violations,
or actions that put users or the project at risk. Whenever practical, such a
change will be communicated privately before it takes effect.

## Releases

Maintainers determine release readiness after CI passes, user-visible changes
are documented, migrations are reviewed, and known security concerns are
addressed. Release notes are derived from [CHANGELOG.md](CHANGELOG.md).
