import type { SettingsPageId, TeamSettingsSection } from '@/lib/app-routes'

export type SettingsSearchItem = {
  title: string
  keywords?: string[]
}

export type SettingsSearchGroup = SettingsSearchItem & {
  items?: SettingsSearchItem[]
}

export type SettingsSearchPage = {
  id: SettingsPageId
  title: string
  section: string
  keywords?: string[]
  groups?: SettingsSearchGroup[]
}

export type SettingsSearchResult = {
  key: string
  page: SettingsPageId
  kind: 'page' | 'section' | 'item'
  title: string
  sectionTitle?: string
  targetTitle?: string
  score: number
}

export type SettingsTeamSearchGroup = SettingsSearchGroup & {
  section: TeamSettingsSection
}

export type SettingsTeamSearchResult = {
  key: string
  teamKey: string
  teamName: string
  kind: 'page' | 'section' | 'item'
  title: string
  targetTitle?: string
  section?: TeamSettingsSection
  score: number
}

type TeamSearchInput = { key: string; name: string }
type TeamSearchDirectory = {
  cache: Map<string, SettingsTeamSearchResult[]>
  normalized: string[]
  teams: TeamSearchInput[]
}

const teamDirectoryCache = new WeakMap<object, TeamSearchDirectory>()

const TEAM_SEARCH_SECTIONS: SettingsTeamSearchGroup[] = [
  { title: 'Overview', section: 'overview', keywords: ['home', 'summary'] },
  {
    title: 'General',
    section: 'general',
    keywords: ['name', 'identifier', 'timezone', 'description', 'estimates'],
    items: [
      { title: 'Team hierarchy', keywords: ['parent team', 'sub-team'] },
      { title: 'Team initiatives', keywords: ['initiatives'] },
      { title: 'Show initiatives in the sidebar', keywords: ['sidebar'] },
      { title: 'Icon & Name', keywords: ['logo', 'avatar'] },
      { title: 'Identifier', keywords: ['team key'] },
      { title: 'Description', keywords: ['team summary'] },
      { title: 'Timezone', keywords: ['time zone'] },
      { title: 'Issue estimation', keywords: ['estimate'] },
      { title: 'Enable detailed issue history', keywords: ['history'] },
      { title: 'Enable issue creation by email', keywords: ['email intake'] },
    ],
  },
  {
    title: 'Security',
    section: 'security',
    keywords: ['visibility', 'permissions', 'access'],
    items: [
      { title: 'Team visibility', keywords: ['private', 'public'] },
      { title: 'Who can join', keywords: ['membership'] },
      { title: 'Management permissions', keywords: ['admin'] },
    ],
  },
  {
    title: 'Members',
    section: 'members',
    keywords: ['people', 'invite', 'roles'],
    items: [{ title: 'Team members', keywords: ['people', 'invite'] }],
  },
  {
    title: 'Notifications',
    section: 'notifications',
    keywords: ['slack', 'updates'],
    items: [
      { title: 'Workspace connection', keywords: ['slack'] },
      { title: 'Channel', keywords: ['slack channel'] },
    ],
  },
  {
    title: 'Issue labels',
    section: 'issue-labels',
    keywords: ['labels', 'tags'],
    items: [{ title: 'Issue labels', keywords: ['tags'] }],
  },
  {
    title: 'Templates',
    section: 'templates',
    keywords: ['issue template', 'project template'],
    items: [
      { title: 'Issue templates', keywords: ['default issue'] },
      { title: 'Project templates', keywords: ['default project'] },
    ],
  },
  {
    title: 'Recurring issues',
    section: 'recurring-issues',
    keywords: ['recurring', 'schedule'],
    items: [{ title: 'Cadence', keywords: ['frequency', 'schedule'] }],
  },
  {
    title: 'Statuses',
    section: 'statuses',
    keywords: ['workflow states'],
    items: [{ title: 'Issue statuses', keywords: ['workflow states'] }],
  },
  {
    title: 'Workflow',
    section: 'workflow',
    keywords: ['automations', 'pull requests', 'stale'],
    items: [
      { title: 'Pull request automations', keywords: ['github', 'gitlab'] },
      { title: 'Branch-specific rules', keywords: ['branch pattern'] },
      { title: 'Release automations', keywords: ['release pipeline'] },
      { title: 'Auto-close parent issues', keywords: ['completed'] },
      { title: 'Auto-close sub-issues', keywords: ['completed'] },
      { title: 'Auto-close stale issues', keywords: ['stale'] },
      { title: 'Auto-archive closed items after', keywords: ['archive'] },
      { title: 'When progressing status, place issues…', keywords: ['ordering'] },
    ],
  },
  {
    title: 'Triage',
    section: 'triage',
    keywords: ['intake', 'prioritization'],
    items: [
      { title: 'Enable triage for this team', keywords: ['triage'] },
      { title: 'Require explicit prioritization', keywords: ['priority'] },
      { title: 'Triage responsibility', keywords: ['owner'] },
      { title: 'Triage rules', keywords: ['automations'] },
    ],
  },
  {
    title: 'Cycles',
    section: 'cycles',
    keywords: ['sprints', 'schedule'],
    items: [
      { title: 'Enable cycles', keywords: ['sprints'] },
      { title: 'Cycle duration', keywords: ['length'] },
      { title: 'Cooldown', keywords: ['break'] },
      { title: 'Capacity', keywords: ['estimate'] },
      { title: 'Upcoming cycles', keywords: ['future'] },
      { title: 'Automatically create upcoming cycles', keywords: ['automation'] },
      { title: 'Add active issues', keywords: ['automation'] },
      { title: 'Add issues with due dates in the cycle', keywords: ['due date'] },
      { title: 'Add started issues', keywords: ['started'] },
      { title: 'Add completed issues', keywords: ['completed'] },
      { title: 'Move unfinished issues to the next cycle', keywords: ['rollover'] },
    ],
  },
  {
    title: 'Agents',
    section: 'agents',
    keywords: ['coding agents', 'loops'],
    items: [
      { title: 'Connected agents', keywords: ['coding agents'] },
      { title: 'Loops', keywords: ['agent automations'] },
    ],
  },
  {
    title: 'Agent skills',
    section: 'agent-skills',
    keywords: ['skills'],
    items: [{ title: 'Agent skills', keywords: ['skills'] }],
  },
  {
    title: 'AI updates',
    section: 'ai-updates',
    keywords: ['project update prompt'],
    items: [{ title: 'Project update prompt', keywords: ['updates'] }],
  },
  {
    title: 'AI summaries',
    section: 'ai-summaries',
    keywords: ['thread summaries'],
    items: [{ title: 'Resolved thread summaries', keywords: ['summaries'] }],
  },
]

export const SETTINGS_SEARCH_PAGES: SettingsSearchPage[] = [
  {
    id: 'preferences',
    title: 'Preferences',
    section: 'Personal',
    keywords: ['account', 'general', 'appearance'],
    groups: [
      {
        title: 'General',
        keywords: ['account'],
        items: [
          { title: 'Language', keywords: ['locale', 'translation'] },
          { title: 'Default home view', keywords: ['landing', 'start page'] },
          { title: 'Display names', keywords: ['username', 'full name'] },
          { title: 'First day of the week', keywords: ['calendar', 'dates'] },
          { title: 'Convert text emoticons into emojis', keywords: ['emoticons', 'emoji'] },
          { title: 'Send comments on…', keywords: ['submit', 'shortcut', 'enter'] },
        ],
      },
      {
        title: 'Interface and theme',
        keywords: ['appearance', 'display', 'dark', 'light', 'color scheme'],
        items: [
          { title: 'App sidebar', keywords: ['navigation', 'sidebar customization'] },
          { title: 'Font size', keywords: ['text size', 'typography'] },
          { title: 'Use pointer cursors', keywords: ['cursor', 'pointer'] },
          { title: 'Underline links', keywords: ['links', 'underline'] },
          { title: 'Disable animated images & emoji', keywords: ['animation', 'gif', 'emoji'] },
          { title: 'Interface theme', keywords: ['dark', 'light', 'system', 'color scheme'] },
        ],
      },
      {
        title: 'Desktop application',
        items: [{ title: 'Open in desktop app', keywords: ['desktop', 'links'] }],
      },
      {
        title: 'Automations and workflows',
        items: [
          { title: 'Auto-assign to self', keywords: ['assignee', 'new issues'] },
          { title: 'On move to started status, assign to yourself', keywords: ['assignee', 'started'] },
        ],
      },
    ],
  },
  {
    id: 'profile',
    title: 'Profile',
    section: 'Personal',
    keywords: ['account', 'identity'],
    groups: [
      {
        title: 'Profile details',
        items: [
          { title: 'Profile picture', keywords: ['avatar', 'image'] },
          { title: 'Email', keywords: ['login', 'address'] },
          { title: 'Change email', keywords: ['login', 'address'] },
          { title: 'Full name', keywords: ['name'] },
          { title: 'Title', keywords: ['job title', 'role'] },
          { title: 'Username', keywords: ['handle', 'nickname'] },
        ],
      },
      {
        title: 'Workspace access',
        items: [{ title: 'Remove yourself from workspace', keywords: ['leave', 'delete workspace'] }],
      },
    ],
  },
  {
    id: 'notifications',
    title: 'Notifications',
    section: 'Personal',
    keywords: ['alerts', 'email', 'desktop', 'mobile'],
    groups: [
      {
        title: 'Push notifications',
        items: [
          { title: 'Desktop', keywords: ['desktop notifications'] },
          { title: 'Mobile', keywords: ['mobile notifications'] },
          { title: 'Email', keywords: ['email notifications'] },
          { title: 'Slack', keywords: ['slack notifications'] },
        ],
      },
      {
        title: 'Updates from Flow',
        items: [
          { title: 'Show updates in sidebar', keywords: ['updates'] },
          { title: 'Changelog newsletter', keywords: ['newsletter'] },
        ],
      },
      {
        title: 'Other updates',
        items: [
          { title: 'Invite accepted', keywords: ['invitation'] },
          { title: 'Privacy and legal updates', keywords: ['legal', 'privacy'] },
          { title: 'Data processing agreement (DPA)', keywords: ['dpa', 'privacy'] },
          { title: 'Marketing and onboarding', keywords: ['marketing'] },
        ],
      },
    ],
  },
  {
    id: 'code-and-reviews',
    title: 'Code & reviews',
    section: 'Personal',
    keywords: ['coding tools', 'git', 'github', 'gitlab', 'pull requests', 'syntax theme'],
    groups: [
      {
        title: 'Code reviews in Flow',
        keywords: ['pull requests'],
        items: [
          { title: 'Enable code reviews', keywords: ['github', 'gitlab', 'inbox'] },
          { title: 'Auto-convert draft pull requests', keywords: ['draft'] },
          { title: 'Merge strategy', keywords: ['squash', 'rebase', 'merge'] },
          { title: 'Code theme', keywords: ['syntax theme', 'highlighting'] },
          { title: 'Font', keywords: ['code font', 'monospace'] },
        ],
      },
      {
        title: 'Notifications',
        items: [
          { title: 'Comments & reviews', keywords: ['inbox', 'mentions'] },
          { title: 'Review requests', keywords: ['inbox'] },
          { title: 'GitHub team review requests', keywords: ['github', 'review'] },
          { title: 'Checks & merge queue', keywords: ['checks', 'merge queue'] },
        ],
      },
      {
        title: 'Signed commits',
        items: [
          { title: 'Require signed commits', keywords: ['git', 'security'] },
          { title: 'Commit signing key', keywords: ['gpg', 'ssh key'] },
        ],
      },
      {
        title: 'External tools',
        items: [
          { title: 'Configure coding tools', keywords: ['ide', 'editor'] },
          { title: 'Git attachment format', keywords: ['attachments'] },
          { title: 'On git branch copy, move issue to started status', keywords: ['branch', 'started'] },
          { title: 'On open in coding tool, move issue to started status', keywords: ['coding tool', 'started'] },
        ],
      },
    ],
  },
  {
    id: 'account-security',
    title: 'Security & access',
    section: 'Personal',
    keywords: ['password', 'passkey', 'sessions', 'api keys', 'devices'],
    groups: [
      {
        title: 'Sessions',
        items: [{ title: 'Active sessions', keywords: ['devices', 'sign out'] }],
      },
      {
        title: 'Passkeys',
        items: [{ title: 'Passkeys', keywords: ['security key', 'biometric'] }],
      },
      {
        title: 'Personal API keys',
        items: [{ title: 'Personal API keys', keywords: ['token', 'developer'] }],
      },
      {
        title: 'Authorized applications',
        items: [{ title: 'Authorized applications', keywords: ['oauth', 'access'] }],
      },
    ],
  },
  {
    id: 'connections',
    title: 'Connected accounts',
    section: 'Personal',
    keywords: ['google', 'github', 'gitlab', 'login'],
    groups: [
      {
        title: 'Sign-in identities',
        items: [
          { title: 'Google', keywords: ['sso', 'login'] },
          { title: 'GitHub', keywords: ['login'] },
          { title: 'GitLab', keywords: ['login'] },
        ],
      },
    ],
  },
  {
    id: 'agents',
    title: 'Agent personalization',
    section: 'Personal',
    keywords: ['ai', 'agent', 'mcp', 'skills'],
    groups: [
      { title: 'Guidance', items: [{ title: 'Guidance', keywords: ['instructions'] }] },
      { title: 'Skills', items: [{ title: 'Skills', keywords: ['agent skills'] }] },
      { title: 'MCP connectors', items: [{ title: 'MCP connectors', keywords: ['model context protocol'] }] },
    ],
  },
  {
    id: 'issue-labels',
    title: 'Labels',
    section: 'Issues',
    keywords: ['issue labels', 'tags', 'groups'],
    groups: [{ title: 'Issue labels', items: [{ title: 'Issue labels', keywords: ['tags'] }] }],
  },
  {
    id: 'issue-templates',
    title: 'Templates',
    section: 'Issues',
    keywords: ['issue templates', 'default issue'],
    groups: [
      {
        title: 'Issue templates',
        items: [
          { title: 'Template name', keywords: ['title'] },
          { title: 'Default status', keywords: ['workflow state'] },
          { title: 'Default assignee', keywords: ['owner'] },
        ],
      },
    ],
  },
  {
    id: 'sla',
    title: 'SLAs',
    section: 'Issues',
    keywords: ['service level agreements', 'response time', 'resolution time'],
    groups: [{ title: 'SLAs', items: [{ title: 'Service level agreements', keywords: ['response', 'resolution', 'time'] }] }],
  },
  {
    id: 'project-labels',
    title: 'Labels',
    section: 'Projects',
    keywords: ['project labels', 'tags', 'groups'],
    groups: [{ title: 'Project labels', items: [{ title: 'Project labels', keywords: ['tags'] }] }],
  },
  {
    id: 'project-templates',
    title: 'Templates',
    section: 'Projects',
    keywords: ['project templates'],
    groups: [{ title: 'Project templates', items: [{ title: 'Project template', keywords: ['template'] }] }],
  },
  {
    id: 'project-statuses',
    title: 'Statuses',
    section: 'Projects',
    keywords: ['project statuses', 'workflow'],
    groups: [
      {
        title: 'Project statuses',
        items: [
          { title: 'Status name', keywords: ['name'] },
          { title: 'Status color', keywords: ['color'] },
          { title: 'Status type', keywords: ['backlog', 'planned', 'started', 'completed', 'canceled'] },
        ],
      },
    ],
  },
  {
    id: 'project-updates',
    title: 'Updates',
    section: 'Projects',
    keywords: ['project updates', 'health', 'reminders'],
    groups: [
      {
        title: 'Project updates',
        items: [
          { title: 'Update schedule', keywords: ['cadence', 'reminder'] },
          { title: 'Health', keywords: ['on track', 'at risk', 'off track'] },
        ],
      },
    ],
  },
  {
    id: 'ai',
    title: 'AI & Agents',
    section: 'Features',
    keywords: ['flow agent', 'ai', 'coding sessions', 'loops'],
    groups: [
      {
        title: 'Flow Agent',
        items: [
          { title: 'Flow Agent', keywords: ['assistant'] },
          { title: 'Code Intelligence', keywords: ['code search'] },
          { title: 'Coding Sessions', keywords: ['agents'] },
          { title: 'Loops', keywords: ['automations'] },
        ],
      },
      {
        title: 'Installed Agents',
        items: [{ title: 'Installed agents guidance', keywords: ['instructions'] }],
      },
      {
        title: 'AI',
        items: [{ title: 'Resolved thread summaries', keywords: ['summaries'] }],
      },
    ],
  },
  {
    id: 'initiatives',
    title: 'Initiatives',
    section: 'Features',
    keywords: ['strategy', 'planning'],
    groups: [
      {
        title: 'Initiatives',
        items: [{ title: 'Enable Initiatives', keywords: ['strategy'] }],
      },
      {
        title: 'Initiative updates',
        items: [
          { title: 'Update schedule', keywords: ['cadence'] },
          { title: 'Send initiative updates to a Slack channel', keywords: ['slack'] },
        ],
      },
    ],
  },
  {
    id: 'initiative-labels',
    title: 'Initiative labels',
    section: 'Features',
    keywords: ['labels', 'tags', 'strategy'],
    groups: [{ title: 'Initiative labels', items: [{ title: 'Initiative labels', keywords: ['tags'] }] }],
  },
  {
    id: 'documents',
    title: 'Documents',
    section: 'Features',
    keywords: ['docs', 'templates'],
    groups: [
      {
        title: 'Templates',
        items: [
          { title: 'Document template', keywords: ['docs'] },
          { title: 'Template name', keywords: ['title'] },
          { title: 'Document template content', keywords: ['content'] },
        ],
      },
    ],
  },
  {
    id: 'customer-requests',
    title: 'Customer requests',
    section: 'Features',
    keywords: ['customers', 'revenue', 'requests'],
    groups: [
      {
        title: 'Customer requests',
        items: [
          { title: 'Enable Customer requests', keywords: ['customers'] },
          { title: 'Manage customers', keywords: ['customer list'] },
        ],
      },
      {
        title: 'Issue routing',
        items: [{ title: 'Default team for customer requests', keywords: ['team'] }],
      },
      {
        title: 'Customer statuses',
        items: [{ title: 'Customer statuses', keywords: ['segments'] }],
      },
      {
        title: 'Customer tiers',
        items: [{ title: 'Customer tiers', keywords: ['segments'] }],
      },
      {
        title: 'Display options',
        items: [
          { title: 'Revenue formatting', keywords: ['annual', 'monthly'] },
          { title: 'Revenue currency', keywords: ['currency'] },
        ],
      },
      {
        title: 'Customer attributes data source',
        items: [
          { title: 'External data source', keywords: ['sync'] },
          { title: 'Enable manual edits', keywords: ['attributes'] },
        ],
      },
      {
        title: 'Excluded domains and emails',
        items: [{ title: 'Excluded domains and emails', keywords: ['domains', 'email'] }],
      },
      {
        title: 'Generic domains and emails',
        items: [{ title: 'Generic domains and emails', keywords: ['domains', 'email'] }],
      },
    ],
  },
  {
    id: 'releases',
    title: 'Releases',
    section: 'Features',
    keywords: ['pipeline', 'changelog'],
    groups: [
      {
        title: 'Releases',
        items: [
          { title: 'Pipeline name', keywords: ['release pipeline'] },
          { title: 'Pipeline state', keywords: ['active', 'deleted'] },
          { title: 'Releases', keywords: ['versions'] },
        ],
      },
    ],
  },
  {
    id: 'pulse',
    title: 'Pulse',
    section: 'Features',
    keywords: ['updates', 'summary'],
    groups: [
      {
        title: 'Pulse',
        items: [{ title: 'Enable Pulse', keywords: ['updates feed'] }],
      },
      {
        title: 'Summary notifications',
        items: [{ title: 'Default workspace schedule', keywords: ['daily', 'weekly'] }],
      },
    ],
  },
  {
    id: 'asks',
    title: 'Asks',
    section: 'Features',
    keywords: ['forms', 'requests', 'email', 'slack'],
    groups: [
      { title: 'Slack', items: [{ title: 'Slack workspace', keywords: ['connect'] }] },
      {
        title: 'Email',
        items: [
          { title: 'Verified email address', keywords: ['email'] },
          { title: 'Add Ask email', keywords: ['email intake'] },
        ],
      },
    ],
  },
  {
    id: 'emojis',
    title: 'Emojis',
    section: 'Features',
    keywords: ['emoji', 'custom reactions'],
    groups: [
      {
        title: 'Emojis',
        items: [
          { title: 'Emoji state', keywords: ['active', 'archived'] },
          { title: 'Upload emoji', keywords: ['custom emoji'] },
        ],
      },
    ],
  },
  {
    id: 'integrations',
    title: 'Integrations',
    section: 'Features',
    keywords: ['apps', 'connectors', 'github', 'gitlab', 'slack'],
    groups: [
      {
        title: 'Integrations',
        items: [
          { title: 'Installed integrations', keywords: ['connected apps'] },
          { title: 'Available integrations', keywords: ['marketplace'] },
        ],
      },
    ],
  },
  {
    id: 'workspace',
    title: 'Workspace',
    section: 'Administration',
    keywords: ['organization', 'logo', 'url', 'region'],
    groups: [
      {
        title: 'Workspace settings',
        items: [
          { title: 'Workspace name', keywords: ['organization'] },
          { title: 'Workspace URL', keywords: ['slug', 'url key'] },
          { title: 'Workspace logo', keywords: ['avatar', 'icon', 'image'] },
          { title: 'Region', keywords: ['data residency'] },
          { title: 'Upload policy', keywords: ['files', 'attachments'] },
          { title: 'Data privacy', keywords: ['privacy', 'retention'] },
        ],
      },
    ],
  },
  {
    id: 'teams',
    title: 'Teams',
    section: 'Administration',
    keywords: ['team hierarchy', 'sub-team', 'members'],
    groups: [
      {
        title: 'Teams',
        items: [
          { title: 'Create a team', keywords: ['new team'] },
          { title: 'Team visibility', keywords: ['private', 'public'] },
          { title: 'Team members', keywords: ['people'] },
        ],
      },
    ],
  },
  {
    id: 'members',
    title: 'Members',
    section: 'Administration',
    keywords: ['people', 'invite', 'roles', 'applications'],
    groups: [
      {
        title: 'Members',
        items: [
          { title: 'Invite members', keywords: ['invite people'] },
          { title: 'Roles', keywords: ['admin', 'member', 'guest'] },
          { title: 'Applications', keywords: ['oauth apps'] },
        ],
      },
    ],
  },
  {
    id: 'security',
    title: 'Security',
    section: 'Administration',
    keywords: ['saml', 'scim', 'sso', 'domain', 'authentication'],
    groups: [
      {
        title: 'Security',
        items: [
          { title: 'SAML', keywords: ['sso', 'identity provider'] },
          { title: 'SCIM', keywords: ['provisioning'] },
          { title: 'Allowed domains', keywords: ['domain'] },
          { title: 'Authentication', keywords: ['login'] },
        ],
      },
    ],
  },
  {
    id: 'audit-log',
    title: 'Audit log',
    section: 'Administration',
    keywords: ['security events', 'activity', 'export'],
    groups: [
      {
        title: 'Audit log',
        items: [
          { title: 'Audit events', keywords: ['activity'] },
          { title: 'Export audit log', keywords: ['csv'] },
        ],
      },
    ],
  },
  {
    id: 'api',
    title: 'API',
    section: 'Administration',
    keywords: ['developer', 'api keys', 'webhooks', 'oauth'],
    groups: [
      {
        title: 'API keys',
        items: [
          { title: 'Personal API keys', keywords: ['token'] },
          { title: 'Workspace API keys', keywords: ['token'] },
          { title: 'Create API key', keywords: ['new key'] },
        ],
      },
      {
        title: 'Signing keys',
        items: [{ title: 'Commit signing key', keywords: ['gpg', 'ssh'] }],
      },
      {
        title: 'Webhooks',
        items: [{ title: 'Webhooks', keywords: ['events', 'http'] }],
      },
    ],
  },
  {
    id: 'applications',
    title: 'Applications',
    section: 'Administration',
    keywords: ['oauth applications', 'authorized apps'],
    groups: [
      {
        title: 'Applications',
        items: [
          { title: 'OAuth applications', keywords: ['developer apps'] },
          { title: 'Authorized applications', keywords: ['oauth access'] },
        ],
      },
    ],
  },
  {
    id: 'import-export',
    title: 'Import & export',
    section: 'Administration',
    keywords: ['csv', 'import', 'export', 'data'],
    groups: [
      {
        title: 'Import & export',
        items: [
          { title: 'Import', keywords: ['csv', 'data'] },
          { title: 'Export', keywords: ['csv', 'data'] },
        ],
      },
    ],
  },
  {
    id: 'workflows',
    title: 'Workflows',
    section: 'Administration',
    keywords: ['automations', 'definitions', 'run history'],
    groups: [
      { title: 'Definitions', items: [{ title: 'Definitions', keywords: ['automations'] }] },
      { title: 'Run history', items: [{ title: 'Run history', keywords: ['automation runs'] }] },
    ],
  },
]

type IndexedSettingsResult = Omit<SettingsSearchResult, 'score'> & {
  normalizedTitle: string
  normalizedOriginalTitle: string
  keywordTokens: string[]
  keywordText: string
  parentText: string
  scoreBonus: number
}

export function createSettingsSearchIndex(
  pages: SettingsSearchPage[],
  translate: (value: string) => string = value => value,
) {
  const entries = buildSettingsSearchEntries(pages, translate)
  const cache = new Map<string, SettingsSearchResult[]>()
  return {
    search(query: string) {
      const normalizedQuery = normalize(query)
      if (!normalizedQuery) return []
      const cached = cache.get(normalizedQuery)
      if (cached) return cached
      const terms = normalizedQuery.split(/\s+/).filter(Boolean)
      const results: SettingsSearchResult[] = []
      const unique = new Map<string, SettingsSearchResult>()
      for (const entry of entries) {
        const score = scoreIndexedEntry(entry, terms)
        if (score <= 0) continue
        const result: SettingsSearchResult = {
          key: entry.key,
          page: entry.page,
          kind: entry.kind,
          title: entry.title,
          sectionTitle: entry.sectionTitle,
          targetTitle: entry.targetTitle,
          score,
        }
        const uniqueKey = `${result.page}:${result.targetTitle ?? result.title}:${result.kind}`
        const current = unique.get(uniqueKey)
        if (!current || result.score > current.score) unique.set(uniqueKey, result)
      }
      results.push(...unique.values())
      results.sort(compareResults)
      const limited = results.slice(0, 30)
      cache.set(normalizedQuery, limited)
      if (cache.size > 32) cache.delete(cache.keys().next().value!)
      return limited
    },
  }
}

export function searchSettings(
  query: string,
  pages: SettingsSearchPage[],
  translate: (value: string) => string = value => value,
): SettingsSearchResult[] {
  return createSettingsSearchIndex(pages, translate).search(query)
}

function buildSettingsSearchEntries(
  pages: SettingsSearchPage[],
  translate: (value: string) => string,
): IndexedSettingsResult[] {
  const entries: IndexedSettingsResult[] = []
  pages.forEach(page => {
    entries.push(indexedEntry({
      key: `${page.id}:page`,
      page: page.id,
      kind: 'page',
      title: page.title,
      sectionTitle: page.section,
    }, page, [page.section], translate))
    page.groups?.forEach((group, groupIndex) => {
      entries.push(indexedEntry({
        key: `${page.id}:section:${groupIndex}`,
        page: page.id,
        kind: 'section',
        title: group.title,
        sectionTitle: page.title,
        targetTitle: group.title,
      }, group, [page.title], translate))
      group.items?.forEach((item, itemIndex) => {
        entries.push(indexedEntry({
          key: `${page.id}:item:${groupIndex}:${itemIndex}`,
          page: page.id,
          kind: 'item',
          title: item.title,
          sectionTitle: group.title,
          targetTitle: item.title,
        }, item, [page.title, group.title], translate, 5))
      })
    })
  })
  return entries
}

function indexedEntry(
  result: Omit<SettingsSearchResult, 'score'>,
  entry: SettingsSearchItem,
  parents: string[],
  translate: (value: string) => string,
  scoreBonus = 0,
): IndexedSettingsResult {
  const localizedTitle = translate(result.title)
  const originalTitle = result.title
  const keywords = normalize((entry.keywords ?? []).join(' '))
  const localizedKeywords = normalize((entry.keywords ?? []).map(translate).join(' '))
  return {
    ...result,
    normalizedTitle: normalize(localizedTitle),
    normalizedOriginalTitle: normalize(originalTitle),
    keywordTokens: [...new Set(`${keywords} ${localizedKeywords}`.split(/\s+/).filter(Boolean))],
    keywordText: `${keywords} ${localizedKeywords}`,
    parentText: normalize(parents.map(translate).join(' ')),
    scoreBonus,
  }
}

export function searchTeams(
  query: string,
  teams: TeamSearchInput[],
  translate: (value: string) => string = value => value,
  localeKey = '',
): SettingsTeamSearchResult[] {
  const normalizedQuery = normalize(query)
  if (!normalizedQuery) return []
  const directory = getTeamSearchDirectory(teams)
  const cacheKey = `${localeKey}\u0000${normalizedQuery}`
  const cached = directory.cache.get(cacheKey)
  if (cached) return cached
  const terms = normalizedQuery.split(/\s+/).filter(Boolean)
  const results: SettingsTeamSearchResult[] = []
  for (let index = 0; index < directory.teams.length && results.length < 30; index += 1) {
    const team = directory.teams[index]
    const normalizedTeam = directory.normalized[index]
    if (!terms.every(term => normalizedTeam.includes(term))) continue
    const normalizedName = normalize(team.name)
    const pageScore = terms.reduce((score, term) => (
      score + (normalizedName === term ? 80 : normalizedName.startsWith(term) ? 55 : 40)
    ), 0)
    if (pageScore > 0) {
      results.push({
        key: `team:${team.key}:page`,
        teamKey: team.key,
        teamName: team.name,
        kind: 'page',
        title: team.name,
        score: pageScore,
      })
    }
  }
  const definitionMatches = buildTeamDefinitionMatches(terms, translate)
  if (results.length < 30 && definitionMatches.length > 0) {
    for (let teamIndex = 0; teamIndex < teams.length && results.length < 30; teamIndex += 1) {
      const team = teams[teamIndex]
      for (const match of definitionMatches) {
        results.push({
          ...match,
          key: `team:${team.key}:${match.key}`,
          teamKey: team.key,
          teamName: team.name,
        })
        if (results.length >= 30) break
      }
    }
  }
  const unique = new Map<string, SettingsTeamSearchResult>()
  results.forEach(result => {
    const key = `${result.teamKey}:${result.targetTitle ?? result.title}:${result.kind}`
    const current = unique.get(key)
    if (!current || result.score > current.score) unique.set(key, result)
  })
  const limited = [...unique.values()].sort((left, right) => right.score - left.score).slice(0, 30)
  directory.cache.set(cacheKey, limited)
  if (directory.cache.size > 32) directory.cache.delete(directory.cache.keys().next().value!)
  return limited
}

function getTeamSearchDirectory(teams: TeamSearchInput[]): TeamSearchDirectory {
  const cached = teamDirectoryCache.get(teams)
  if (cached) return cached
  const directory = {
    cache: new Map<string, SettingsTeamSearchResult[]>(),
    normalized: teams.map(team => normalize(`${team.name} ${team.key}`)),
    teams,
  }
  teamDirectoryCache.set(teams, directory)
  return directory
}

function buildTeamDefinitionMatches(
  terms: string[],
  translate: (value: string) => string,
): Array<Omit<SettingsTeamSearchResult, 'teamKey' | 'teamName'>> {
  const results: Array<Omit<SettingsTeamSearchResult, 'teamKey' | 'teamName'>> = []
  TEAM_SEARCH_SECTIONS.forEach((group, groupIndex) => {
    const groupScore = scoreEntry(group, group.title, terms, translate, ['Teams'])
    if (groupScore > 0) {
      results.push({
        key: `section:${groupIndex}`,
        kind: 'section',
        title: group.title,
        targetTitle: group.title,
        section: group.section,
        score: groupScore,
      })
    }
    group.items?.forEach((item, itemIndex) => {
      const itemScore = scoreEntry(item, item.title, terms, translate, ['Teams', group.title])
      if (itemScore > 0) {
        results.push({
          key: `item:${groupIndex}:${itemIndex}`,
          kind: 'item',
          title: item.title,
          targetTitle: item.title,
          section: group.section,
          score: itemScore + 5,
        })
      }
    })
  })
  return results
}

function scoreIndexedEntry(entry: IndexedSettingsResult, terms: string[]) {
  let score = 0
  let directMatch = false
  for (const term of terms) {
    if (entry.normalizedTitle === term || entry.normalizedOriginalTitle === term) {
      score += 80
      directMatch = true
    } else if (entry.normalizedTitle.startsWith(term) || entry.normalizedOriginalTitle.startsWith(term)) {
      score += 55
      directMatch = true
    } else if (entry.normalizedTitle.includes(term) || entry.normalizedOriginalTitle.includes(term)) {
      score += 40
      directMatch = true
    } else if (entry.keywordTokens.some(keyword => keyword.startsWith(term))) {
      score += 24
      directMatch = true
    } else if (entry.keywordText.includes(term)) {
      score += 18
      directMatch = true
    } else if (entry.parentText.includes(term)) {
      score += 8
    } else {
      return 0
    }
  }
  return directMatch ? score + entry.scoreBonus : 0
}

function scoreEntry(
  entry: SettingsSearchItem,
  title: string,
  terms: string[],
  translate: (value: string) => string,
  parents: string[],
) {
  const localizedTitle = translate(title)
  const normalizedTitle = normalize(localizedTitle)
  const normalizedOriginalTitle = normalize(title)
  const keywords = normalize([
    ...(entry.keywords ?? []),
    ...(entry.keywords ?? []).map(translate),
  ].join(' '))
  const parentText = normalize(parents.map(translate).join(' '))

  let score = 0
  let directMatch = false
  for (const term of terms) {
    if (normalizedTitle === term || normalizedOriginalTitle === term) {
      score += 80
      directMatch = true
    } else if (normalizedTitle.startsWith(term) || normalizedOriginalTitle.startsWith(term)) {
      score += 55
      directMatch = true
    } else if (normalizedTitle.includes(term) || normalizedOriginalTitle.includes(term)) {
      score += 40
      directMatch = true
    } else if (keywords.split(/\s+/).some(keyword => keyword.startsWith(term))) {
      score += 24
      directMatch = true
    } else if (keywords.includes(term)) {
      score += 18
      directMatch = true
    } else if (parentText.includes(term)) {
      score += 8
    }
    else return 0
  }
  return directMatch ? score : 0
}

function compareResults(left: SettingsSearchResult, right: SettingsSearchResult) {
  if (right.score !== left.score) return right.score - left.score
  return left.key.localeCompare(right.key)
}

function normalize(value: string) {
  return value.trim().toLocaleLowerCase()
}
