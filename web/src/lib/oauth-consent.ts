/** OAuth consent (/oauth/authorize) wording shared by the page and its tests. */

type Translate = (source: string) => string

/** The reference app lists `write` alone when it is requested: the narrower write scopes it covers are not repeated. */
export function consentPermissions(scopes: string[], labels: string[] = []) {
  const covered = scopes.includes('write') ? new Set(['create_issues', 'create_comments', 'timeSchedule:write']) : new Set<string>()
  return scopes.map((scope, index) => ({ scope, label: OAUTH_SCOPE_LABELS[scope] || labels[index] || scope })).filter(item => !covered.has(item.scope))
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
  'initiative:write': 'Create and modify initiatives in the workspace',
  'customer:read': 'Read data about customers in the workspace',
  'customer:write': 'Create and modify customers in the workspace',
  'release:read': 'Read data about releases in the workspace',
  'release:create': 'Create releases in the workspace',
  'releaseStage:update': 'Move releases between stages in the workspace',
  openid: 'Verify your identity',
  email: 'Read your email address',
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

