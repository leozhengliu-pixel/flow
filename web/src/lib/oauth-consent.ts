/** OAuth consent (/oauth/authorize) wording shared by the page and its tests. */

type Translate = (source: string) => string

/**
 * Not an OAuth scope: the reference app lists "Receive realtime updates about your workspace" whenever the application has a
 * webhook (after the requested scopes on the consent screen, and among the permissions in Settings › Applications).
 */
export const REALTIME_PERMISSION = 'realtime'

const WRITE_COVERS = ['create_issues', 'create_comments', 'timeSchedule:write']

/** The reference app lists `write` alone when it is requested: the narrower write scopes it covers are not repeated. */
export function consentPermissions(scopes: string[], labels: string[] = []) {
  const covered = scopes.includes('write') ? new Set(WRITE_COVERS) : new Set<string>()
  return scopes.map((scope, index) => ({ scope, label: OAUTH_SCOPE_LABELS[scope] || labels[index] || scope })).filter(item => !covered.has(item.scope))
}

/** The reference app's team access choices for an app user: all public teams, all public teams and select other (private) teams, only select teams. */
export type TeamMode = 'public' | 'mixed' | 'selected'

/** The picked teams kept when the team access option changes: none for public teams, the private ones for "and select other teams". */
export function nextTeamSelection(mode: TeamMode, teamIds: string[], teams: { id: string; private?: boolean }[]) {
  if (mode === 'public') return []
  if (mode === 'mixed') return teamIds.filter(id => teams.find(team => team.id === id)?.private)
  return teamIds
}

/** The state of an actor=app consent for an application already installed in the workspace. */
export type InstalledConsent = { installedBy: string; installerName: string; createdAt: string; scopes: string[]; realtime: boolean }

/**
 * The reference app's consent screen for an installed application: "Existing permissions" are the installed scopes
 * (alphabetical by wording, without the realtime permission), "Requested permissions" the requested scopes it does not have yet
 * (in request order) and the realtime permission when the application has a webhook the installation was not
 * authorized for. Nothing requested means "<App> already installed" with Continue; otherwise "Update access".
 */
export function reconsentPermissions(installed: InstalledConsent, requested: string[], realtime: boolean) {
  const existing = consentPermissions(installed.scopes).sort((a, b) => a.label.localeCompare(b.label, 'en'))
  const covered = (scope: string) => installed.scopes.includes(scope) || installed.scopes.includes('write') && WRITE_COVERS.includes(scope)
  const added = consentPermissions(requested.filter(scope => !covered(scope)))
  if (realtime && !installed.realtime) added.push({ scope: REALTIME_PERMISSION, label: OAUTH_SCOPE_LABELS[REALTIME_PERMISSION] })
  return { existing, requested: added }
}

/** The reference app's permission wording; the server sends the same text as `scopeLabels`. */
export const OAUTH_SCOPE_LABELS: Record<string, string> = {
  read: 'Read access to your workspace',
  write: 'Write access to your workspace',
  admin: 'Admin access to manage settings and people',
  create_issues: 'Create issues for your workspace',
  create_comments: 'Create issue comments for your workspace',
  'timeSchedule:write': 'Create and modify time schedules for your workspace',
  'app:assignable': 'Assign issues and projects to the app in teams it can access',
  'app:mentionable': 'Mention app in issues, projects, and documents it can access',
  'initiative:read': 'Read data about initiatives in the workspace',
  'initiative:write': 'Read and write data about initiatives in the workspace',
  'customer:read': 'Read data about customers in the workspace',
  'customer:write': 'Read and write data about customers in the workspace',
  'release:read': 'Read releases in the workspace',
  'release:create': 'Create releases in the workspace',
  'releaseStage:update': 'Update release stages in the workspace',
  openid: 'Verify your identity',
  email: 'Read your email address',
  [REALTIME_PERMISSION]: 'Receive realtime updates about your workspace',
}

const OAUTH_ERRORS: Array<[RegExp, string]> = [
  [/^Could not find OAuth client with clientId (.*)\. Please check that you are logged into the correct workspace\.$/, 'Could not find OAuth client with clientId {value}. Please check that you are logged into the correct workspace.'],
  [/^Invalid scope: (.+)$/, 'Invalid scope: {value}'],
]

/** The server's error description, translated. */
export function oauthErrorMessage(value: unknown, t: Translate = source => source) {
  const message = value instanceof Error && value.message ? value.message : t('This authorization request could not be loaded.')
  for (const [pattern, template] of OAUTH_ERRORS) {
    const match = message.match(pattern)
    if (match) return t(template).replace('{value}', match[1])
  }
  return t(message)
}

