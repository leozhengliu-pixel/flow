# Privacy and data subject rights

Flow keeps personal data about the people who use it: their account, how they
sign in, their settings and private work (such as Flow Agent conversations), and
the workspace content they write. This page describes what a person can do with
their own data, what workspace admins can do for them, and exactly what happens
to the data. It is written for operators answering access, portability and
erasure requests (GDPR Articles 15, 17 and 20; PIPL Articles 44–47); it is not
legal advice.

## Access and portability: download your data

**Account › Security & access › Your data › Download your data** (or
`POST /api/account/data-exports`) prepares a zip of JSON files with the signed-in
person's own data, in the background. The zip holds:

| File | Contents |
|---|---|
| `account.json` | Profile, account creation date, the workspaces the person belongs to and their role |
| `identities.json` | Linked sign-in methods (provider, issuer, subject, username, dates). Identity provider claims are never included |
| `sessions.json` | Signed-in sessions and devices. Session tokens are never included |
| `sign-in-notifications.json` | Sign-in alerts sent to the person |
| `connections.json` | Personal connections (calendar, Notion), code host accounts and linked chat accounts. Tokens are never included |
| `workspaces/<key>/settings.json` | Profile settings, notification preferences, push subscriptions (without their keys) |
| `workspaces/<key>/agent-chats.json` | The person's Flow Agent conversations (private to them, with full message bodies), agent skills and AI conversations |
| `workspaces/<key>/api-keys.json` | API keys acting as the person: name, prefix, scopes, dates. Never the secret or its hash |
| `workspaces/<key>/passkeys.json` | Passkeys (name, dates), without credential material |
| `workspaces/<key>/oauth-grants.json` | Applications the person authorized, with scopes |
| `workspaces/<key>/notifications.json` | The person's inbox |
| `workspaces/<key>/search-history.json`, `recently-viewed.json` | Recent searches and what they opened |
| `workspaces/<key>/personal.json` | Favorites, private views, drafts, subscriptions |
| `workspaces/<key>/issues.json`, `comments.json`, `documents.json` | Issues and documents they created and comments they wrote, **limited to what they can still access** |

Workspaces are those the person is an active member of. Content written by
other people is not included, and content in teams the person can no longer
see is left out.

- **Who can download it.** Only the person who requested it, from a signed-in
  session. `/api/account` is closed to API keys and OAuth tokens (as for every
  account-security route).
- **Expiry.** The download link works for **7 days**; storage maintenance then
  deletes the archive and the export shows as expired.
- **Rate limit.** At most **3 exports per account per 24 hours**, and one at a
  time (a request while one is being prepared returns that one). Over the
  limit the API answers `429` with `Retry-After`.
- **Audit.** Each request adds an `account.data_export_requested` entry to the
  audit log of every workspace the person is an active member of.
- **Storage.** Archives are kept in the configured object storage
  (`FLOW_STORAGE_DRIVER`) as `personal_data_export_<id>.zip`; no content links
  to them, so `/uploads/` never serves them.

The workspace export (`POST /api/exports`) is a different thing: it exports the
workspace, as the requesting admin or member can see it, not a person's data.

## Erasure

Erasure deletes a person's **personal data** and keeps the **workspace content
they authored**, which belongs to the workspace, attributed to a pseudonymous
**"Deleted user"**.

### What is deleted

- The account's profile: name, email address, avatar, password, email
  verification. The address is free again: it can sign up as a new, unrelated
  account.
- Sign-in methods (password, SSO/OIDC/SAML identities, SCIM links), all
  sessions and devices, login and reset tokens, passkeys.
- API keys acting as the person, and OAuth applications they authorized (their
  tokens stop working with them). Application keys an admin created for the
  workspace stay: they act as the application.
- Linked chat accounts (Lark, Feishu, Slack) and their pending actions,
  personal connections (calendar, Notion) and code host accounts with their
  stored tokens, the commit signing key.
- Private Flow Agent conversations (with their attachments and run records),
  agent skills, AI conversations.
- Settings and notification preferences, push subscriptions, notifications
  and their deliveries, search history, recently viewed, favorites, private
  views, drafts, subscriptions, the person's import and export jobs.
- Invitations sent to the person's address.

### What stays, and how it is shown

Issues, comments, documents, projects, updates and history the person wrote stay
where they are. They now show the person as **Deleted user**: the same stable
id as before (an opaque random account id such as `usr_…`, which never carried
personal data), a neutral handle (`deleted-user-<last 8 characters of the id>`),
no name, no email and no avatar, and `"deleted": true` in API responses.
Mentions of the person render as "Deleted user".

Flow shows people from the workspace directory, but stored records also keep
copies of the people they name (an issue's creator, a comment's author, a
mention's label, an audit entry's actor, the payload of the events about the
person). Erasure rewrites every such copy in the workspace's issue, content and
metadata records and in its retained domain events, so the name and email are
not left at rest. Free text that someone else typed (for example a name
written out in a comment) is the author's content and is not changed.

A deleted person counts as a former member: they cannot be newly assigned,
mentioned, subscribed or made a lead, and receive nothing. Settings › Members
lists them under "Left workspace" as "Deleted user".

### Grace period

None. Deletion is **immediate** once confirmed; there is no undo. Backups taken
before the deletion still contain the data until they rotate out under your
backup retention (see the backup boundary in [Operations](operations.md)):
restoring one brings the account back, so re-run the erasure after a restore.

## Deleting your own account

**Account › Security & access › Danger zone › Delete account** (or `DELETE /api/account`
with `{"confirmation": "<your email>"}`; an account without an email confirms
with its display name). The person types their email to confirm. On success the
session cookie is cleared and every other session is signed out.

**Last owner or admin.** An account that is the only active owner or admin of a
workspace cannot be deleted: the API answers `409` with
`{"code": "LAST_ADMIN", "workspaces": [{"urlKey": …, "name": …}]}`. Make
someone else an admin of those workspaces, or delete them, first.

Deleting the account erases the person from **every** workspace they belong to
or left, as described above, and then deletes the account itself. An audit
entry (`workspace_member.erased`, by "Deleted user") is added in each
workspace.

## Erasing a member (workspace admins)

**Settings › Members › … › Erase personal data…** (or
`POST /api/workspaces/{workspaceKey}/members/{userId}/erase` with
`{"confirmation": "<member's email>"}`) erases a current or former member of the
admin's workspace. The admin types the member's email to confirm.

- **Account only in this workspace** (`"scope": "account"`): the account has no
  other use, so it is deleted as a whole, exactly as if the person had deleted
  it (they are signed out everywhere).
- **Account also in other workspaces** (`"scope": "workspace"`): only this
  workspace's personal data is erased: their membership and team memberships,
  SCIM links for this workspace, everything listed under "What is deleted"
  that lives in this workspace (settings, agent chats, keys and grants for this
  workspace, notifications, search history, recently viewed, chat accounts
  linked in this workspace, invitations), and the copies of their identity in
  this workspace's records. The account, its sign-in methods and the other
  workspaces are not touched, and it keeps its name there. Within this
  workspace the person stays "Deleted user" with the same id, so the account
  **cannot join this workspace again** (accepting an invitation, joining by
  invite link and SCIM provisioning are refused with `409`; SSO sign-in does
  not provision it): its old content would otherwise be attributed to it
  again. The person can use another account.

Rules: only workspace admins and owners can erase; only an owner can erase
another owner; nobody erases themselves this way (use account deletion); the
last active admin or owner cannot be erased; members managed by SCIM are erased
after the identity provider deprovisions them. Each erasure adds a
`workspace_member.erased` audit entry by the admin.

## SCIM

SCIM `DELETE /Users/{id}` **deprovisions**: it suspends the member, deactivates
the account and unlinks its SCIM identity, keeping the data (RFC 7644 leaves what DELETE does to
the service provider, and identity providers send it routinely when someone
leaves). It does **not** erase. To erase a deprovisioned person, an admin uses
"Erase personal data" afterwards (while SCIM still manages a member, Flow refuses
to erase them, as it refuses to suspend or remove them). Flow does not add an
erasure hook to SCIM, so a routine deprovisioning in the identity provider can
never delete data.

## Retention of other data

- Audit log entries keep the pseudonymous id of the person; their name, email,
  avatar, client address and browser are removed from the entries (for an
  account deletion in every workspace's log and the instance log, for an
  admin's erasure in that workspace's log), and the log's hash chain still
  verifies: each rewrite is recorded by a `personal_data_redacted` entry. See
  [Audit log](operations.md#audit-log) for how the chain covers erased
  entries. Entries already sent to an audit log stream (SIEM) stay there.
- "Download your data" archives are deleted with the account (a job still
  being prepared deletes its archive when it finishes). When a workspace is
  purged after its deletion grace period, the archives of its current and
  former members are deleted too, since they hold the workspace's data; the
  exports show as expired.
- Domain events are pruned after `FLOW_DOMAIN_EVENT_RETENTION_DAYS` (90 by
  default); retained events about an erased person lose their personal fields.
- Search index documents are rebuilt from records as they change; they hold
  issue and document text, not people's profiles.

## API reference

| Route | Who | Purpose |
|---|---|---|
| `GET /api/account/data-exports` | signed-in session | The person's exports, newest first (`status`: `queued`, `running`, `completed`, `failed`, `expired`; `downloadUrl` while completed) |
| `POST /api/account/data-exports` | signed-in session | Request an export (`202`); `429` over the limit |
| `GET /api/account/data-exports/{id}/download` | the export's owner | The zip (`application/zip`); `409` while preparing, `410` once expired |
| `DELETE /api/account` | signed-in session | Delete the account; body `{"confirmation"}`; `204`, `400` on a wrong confirmation, `409` `LAST_ADMIN` |
| `POST /api/workspaces/{workspaceKey}/members/{userId}/erase` | workspace admin | Erase a member; body `{"confirmation"}`; `200` `{"userId","scope"}` |

These routes are part of the web app's internal API (see
[openapi.json](openapi.json), `x-flow-internal`); none of them accepts API keys
except the member erasure, which needs an `admin`-scoped key like the other
member routes.
