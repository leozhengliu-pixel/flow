import { describe, expect, it } from 'vitest'
import { pageKind, supportReportUrl } from './support-report'

describe('support report', () => {
  it('names the page kind without the path, ids or workspace key', () => {
    expect(pageKind('/acme-internal/settings/api')).toBe('Settings')
    expect(pageKind('/acme-internal/issue/ENG-12/title')).toBe('Issue')
    expect(pageKind('/acme-internal/whatever/secret')).toBe('Other')
  })

  it('keeps hostnames, workspace keys and ids out of the prefilled issue', () => {
    const url = supportReportUrl({ title: 'T', browser: 'Browser:', page: 'Page:', describe: 'Describe:' }, '/acme-internal/issue/ENG-12/title')
    const body = new URL(url).searchParams.get('body')!
    expect(body).toContain('Page: Issue')
    expect(body).not.toMatch(/acme-internal|ENG-12|localhost|127\.0\.0\.1/)
  })
})
