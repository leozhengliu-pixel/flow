const REPOSITORY = 'https://github.com/leozhengliu-pixel/flow'

// Page kinds that may be named in a public report. The path itself (workspace key, ids, host) never is.
const PAGE_KINDS: Record<string, string> = {
  settings: 'Settings', issue: 'Issue', team: 'Team', project: 'Project', projects: 'Projects', views: 'Views',
  inbox: 'Inbox', agent: 'Agent', reviews: 'Reviews', initiatives: 'Initiatives', 'my-issues': 'My issues', docs: 'Documents',
}

/** The kind of page a path shows ("Settings", "Issue"), or "Other"; never the path, ids or workspace key. */
export function pageKind(pathname: string) {
  const segments = pathname.split('/').filter(Boolean)
  return PAGE_KINDS[segments[1] ?? ''] ?? 'Other'
}

/** A pre-filled new-issue URL for the public repository with browser and page kind only, so submitting it unedited leaks no hostnames, workspace keys or ids. */
export function supportReportUrl(labels: { title: string; browser: string; page: string; describe: string }, pathname = location.pathname) {
  const url = new URL(`${REPOSITORY}/issues/new`)
  url.searchParams.set('title', labels.title)
  url.searchParams.set('body', `${labels.browser} ${navigator.userAgent}\n${labels.page} ${pageKind(pathname)}\n\n${labels.describe}\n`)
  return url.toString()
}
