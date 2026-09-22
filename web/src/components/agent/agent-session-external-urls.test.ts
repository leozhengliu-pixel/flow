import { describe, expect, it } from 'vitest'
import {
  externalUrlsFromActivities,
  filteredExternalUrls,
  labelForExternalUrl,
} from './agent-session-external-urls'

describe('LS-0039 agent session external urls', () => {
  it('filters and dedupes http(s) urls', () => {
    expect(
      filteredExternalUrls([
        'https://github.com/acme/flow/pull/1',
        'https://github.com/acme/flow/pull/1/',
        'ftp://example.com',
        { url: 'https://www.figma.com/file/abc', label: 'Design' },
        null,
      ]),
    ).toEqual([
      { url: 'https://github.com/acme/flow/pull/1', label: 'GitHub' },
      { url: 'https://www.figma.com/file/abc', label: 'Design' },
    ])
  })

  it('labels known hosts', () => {
    expect(labelForExternalUrl('https://gitlab.com/group/repo')).toBe('GitLab')
  })

  it('extracts urls from activities', () => {
    const urls = externalUrlsFromActivities([
      { url: 'https://vercel.com/deploy/1', body: 'done' },
      { body: 'See https://github.com/acme/flow and https://github.com/acme/flow.' },
    ])
    expect(urls.map(item => item.label)).toEqual(['Vercel', 'GitHub'])
  })
})
