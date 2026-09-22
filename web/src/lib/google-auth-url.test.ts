import { describe, expect, it } from 'vitest'
import {
  GOOGLE_SCOPE_CALENDAR_READONLY,
  GOOGLE_SCOPE_DRIVE_FILE,
  GOOGLE_SCOPE_USERINFO_EMAIL,
  buildGoogleAuthUrl,
  googleCalendarAuthUrl,
  googleDriveAuthUrl,
  googleLoginAuthUrl,
} from './google-auth-url'

describe('googleAuthUrl (LS-0702)', () => {
  it('builds login URL with profile scopes', () => {
    const url = googleLoginAuthUrl({
      clientId: 'cid',
      redirectUri: 'https://app.example/api/auth/google/callback',
      state: 'abc',
    })
    const parsed = new URL(url)
    expect(parsed.origin + parsed.pathname).toBe(
      'https://accounts.google.com/o/oauth2/v2/auth',
    )
    expect(parsed.searchParams.get('client_id')).toBe('cid')
    expect(parsed.searchParams.get('scope')).toContain(GOOGLE_SCOPE_USERINFO_EMAIL)
    expect(parsed.searchParams.get('access_type')).toBe('offline')
    expect(parsed.searchParams.get('response_type')).toBe('code')
    expect(parsed.searchParams.get('state')).toBe('abc')
  })

  it('builds calendar and drive integration URLs with distinct scopes', () => {
    const calendar = googleCalendarAuthUrl({
      clientId: 'cid',
      redirectUri: 'https://app.example/api/integrations/google-calendar/oauth/callback',
      loginHint: 'user@example.com',
    })
    const drive = googleDriveAuthUrl({
      clientId: 'cid',
      redirectUri: 'https://app.example/api/integrations/google-sheets/oauth/callback',
    })
    expect(new URL(calendar).searchParams.get('scope')).toBe(GOOGLE_SCOPE_CALENDAR_READONLY)
    expect(new URL(calendar).searchParams.get('login_hint')).toBe('user@example.com')
    expect(new URL(drive).searchParams.get('scope')).toBe(GOOGLE_SCOPE_DRIVE_FILE)
  })

  it('honors includeGrantedScopes', () => {
    const url = buildGoogleAuthUrl({
      clientId: 'cid',
      redirectUri: 'https://cb',
      scope: [GOOGLE_SCOPE_DRIVE_FILE],
      includeGrantedScopes: true,
    })
    expect(new URL(url).searchParams.get('include_granted_scopes')).toBe('true')
  })
})
