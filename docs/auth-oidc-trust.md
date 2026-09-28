# Auth OIDC trust boundary (LS-0081)

Flow completes Google / OIDC login **on the server** (`GET /api/auth/{provider}/callback` → `finishOIDC`).

## Server trust (source of truth)

- PKCE `code_verifier`, nonce, and OAuth `state` live in the HttpOnly `flow_external_auth` cookie.
- Token exchange and ID-token verification happen only on the API.
- Successful callbacks set the Flow session cookie and redirect to the client completion route.

## Client state mirror (optional)

- Before navigating to `/api/auth/google/start`, the web client stores `GOOGLE_LOGIN_STATE_KEY` in session/local storage and passes `client_key` as a query param.
- The server echoes `client_key` on the post-login redirect to `/auth/google/callback`.
- The client page verifies the mirror key, then continues to `/` or `/mobile-auth`.
- Mirror mismatch → unified `/auth/error`.

## Errors

All browser-facing OIDC / token-login failures should land on **`/auth/error`** (query `error=` or router state).

## Mobile

`/mobile-auth` is a web stub until a native scheme exists. Token and Google completion paths already branch when `isMobileAppLogin` / `mobileRedirectUri` are present.
