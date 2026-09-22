/**
 * LS-0702 googleAuthUrl — client URL factory for Google OAuth.
 * Login scopes vs calendar.readonly / drive.file integration scopes.
 * Do not reuse the login redirect for calendar/drive.
 */

export const GOOGLE_OAUTH_AUTH_URL = 'https://accounts.google.com/o/oauth2/v2/auth'

export const GOOGLE_SCOPE_USERINFO_EMAIL = 'https://www.googleapis.com/auth/userinfo.email'
export const GOOGLE_SCOPE_USERINFO_PROFILE = 'https://www.googleapis.com/auth/userinfo.profile'
export const GOOGLE_SCOPE_CALENDAR_READONLY = 'https://www.googleapis.com/auth/calendar.readonly'
export const GOOGLE_SCOPE_DRIVE_FILE = 'https://www.googleapis.com/auth/drive.file'

export type GoogleAuthUrlOptions = {
  clientId: string
  redirectUri: string
  scope: string[]
  state?: string
  loginHint?: string
  prompt?: string[]
  includeGrantedScopes?: boolean
  accessType?: 'online' | 'offline'
}

function toQuery(params: Record<string, string | undefined>): string {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== '') search.set(key, value)
  }
  const encoded = search.toString()
  return encoded ? `?${encoded}` : ''
}

/** Low-level builder — accounts.google.com/o/oauth2/v2/auth */
export function buildGoogleAuthUrl(options: GoogleAuthUrlOptions): string {
  const prompt = (options.prompt ?? ['consent']).join(' ')
  return `${GOOGLE_OAUTH_AUTH_URL}${toQuery({
    client_id: options.clientId,
    redirect_uri: options.redirectUri,
    scope: options.scope.join(' '),
    include_granted_scopes:
      options.includeGrantedScopes === undefined
        ? undefined
        : options.includeGrantedScopes
          ? 'true'
          : 'false',
    access_type: options.accessType ?? 'offline',
    response_type: 'code',
    state: options.state,
    prompt,
    login_hint: options.loginHint,
  })}`
}

export function googleLoginAuthUrl(input: {
  clientId: string
  redirectUri: string
  state?: string
  selectAccount?: boolean
}): string {
  return buildGoogleAuthUrl({
    clientId: input.clientId,
    redirectUri: input.redirectUri,
    scope: [GOOGLE_SCOPE_USERINFO_EMAIL, GOOGLE_SCOPE_USERINFO_PROFILE],
    state: input.state,
    prompt: input.selectAccount ? ['select_account', 'consent'] : ['consent'],
  })
}

export function googleCalendarAuthUrl(input: {
  clientId: string
  redirectUri: string
  loginHint?: string
}): string {
  return buildGoogleAuthUrl({
    clientId: input.clientId,
    redirectUri: input.redirectUri,
    scope: [GOOGLE_SCOPE_CALENDAR_READONLY],
    loginHint: input.loginHint,
  })
}

export function googleDriveAuthUrl(input: {
  clientId: string
  redirectUri: string
  loginHint?: string
}): string {
  return buildGoogleAuthUrl({
    clientId: input.clientId,
    redirectUri: input.redirectUri,
    scope: [GOOGLE_SCOPE_DRIVE_FILE],
    loginHint: input.loginHint,
  })
}
