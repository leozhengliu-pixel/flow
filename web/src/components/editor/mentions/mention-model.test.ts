import type { JSONContent } from '@tiptap/core'
import { beforeEach, describe, expect, it } from 'vitest'
import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { makeIssue } from '@/test/fixtures'
import { mentionFixture, mentionLabels, mentionUrls } from './mention-fixtures'
import { convertMentionLinks, linkMentionTarget, mentionAttrsForEntity, mentionKind, mentionMarkdown, mentionTarget, mentionText, typedIdentifierTarget } from './mention-model'

const doc = (...content: JSONContent[]): JSONContent => ({ type: 'doc', content: [{ type: 'paragraph', content }] })
const link = (href: string, text: string): JSONContent => ({ type: 'text', text, marks: [{ type: 'link', attrs: { href } }] })
const paragraph = (converted: JSONContent) => converted.content?.[0].content ?? []

beforeEach(() => resetAgentRecordCache())

describe('mention markdown and plain text', () => {
  it('writes people as @name and every other resource as a link with its path', () => {
    expect(mentionMarkdown({ mentionType: 'user', label: 'Ada' })).toBe('@Ada')
    expect(mentionMarkdown({ mentionType: 'issue', label: 'TST-1', href: '/workspace/issue/TST-1/test-issue' })).toBe('[TST-1](/workspace/issue/TST-1/test-issue)')
    expect(mentionMarkdown({ mentionType: 'document', label: 'Plan [v2] (draft)', href: '/workspace/document/plan abc' })).toBe('[Plan \\[v2\\] (draft)](/workspace/document/plan%20abc)')
    expect(mentionMarkdown({ mentionType: 'project', label: 'No path' })).toBe('No path')
  })

  it('reads as the label in plain text, with @ for people', () => {
    expect(mentionText({ mentionType: 'user', label: 'Ada' })).toBe('@Ada')
    expect(mentionText({ mentionType: 'cycle', label: 'Cycle 3' })).toBe('Cycle 3')
  })

  it('names the kind from mentionType, falling back to the kind an old URL mention points at', () => {
    const data = mentionFixture()
    expect(mentionKind({ mentionType: 'milestone' }, data)).toBe('milestone')
    expect(mentionKind({ mentionType: 'entity', href: mentionUrls.document, label: 'x' }, data)).toBe('document')
    expect(mentionKind({ mentionType: 'entity' })).toBeUndefined()
    expect(mentionTarget({ mentionType: 'issue', id: 'issue-1', label: 'TST-1' }, data)).toEqual({ kind: 'issue', id: 'issue-1', label: 'TST-1' })
  })
})

describe('links to every resource type become mentions', () => {
  const data = mentionFixture()
  const kinds = Object.keys(mentionUrls).filter(kind => kind !== 'user') as Array<keyof typeof mentionUrls>

  for (const kind of kinds) {
    it(`${kind}: a link whose text is the URL converts and carries the kind, label and path`, () => {
      const url = mentionUrls[kind]
      const converted = convertMentionLinks(doc({ type: 'text', text: 'See ' }, link(url, url)), data)
      expect(converted.changed).toBe(true)
      const [, mention] = paragraph(converted.content)
      expect(mention.type).toBe('mention')
      expect(mention.attrs).toMatchObject({ mentionType: kind, label: mentionLabels[kind], href: url.split('#')[0] ? expect.stringContaining(url.split('#')[0]) : url })
      expect(mentionMarkdown(mention.attrs ?? {})).toContain(`[${mentionLabels[kind]}](`)
    })
  }

  it('a link with the resource title converts, one with its own words stays a link', () => {
    expect(paragraph(convertMentionLinks(doc(link(mentionUrls.document, 'Launch plan')), data).content)[0].type).toBe('mention')
    expect(paragraph(convertMentionLinks(doc(link(mentionUrls.issue, 'TST-1')), data).content)[0].type).toBe('mention')
    expect(paragraph(convertMentionLinks(doc(link(mentionUrls.issue, 'TST-1 Test issue')), data).content)[0].type).toBe('mention')
    expect(convertMentionLinks(doc(link(mentionUrls.document, 'the rollout plan')), data).changed).toBe(false)
  })

  it('markdown written by an agent converts whatever its link text says', () => {
    const converted = convertMentionLinks(doc(link(mentionUrls.document, 'the rollout plan')), data, { anyText: true })
    expect(paragraph(converted.content)[0].attrs).toMatchObject({ mentionType: 'document', id: 'document-1', label: 'Launch plan' })
  })

  it('leaves other hosts, other workspaces, unknown resources, code and code blocks alone', () => {
    expect(convertMentionLinks(doc(link('https://elsewhere.test/workspace/document/plan-abc', 'https://elsewhere.test/workspace/document/plan-abc')), data).changed).toBe(false)
    expect(convertMentionLinks(doc(link('/other/document/plan-abc', '/other/document/plan-abc')), data).changed).toBe(false)
    expect(convertMentionLinks(doc(link('/workspace/document/missing', '/workspace/document/missing')), data).changed).toBe(false)
    expect(convertMentionLinks(doc({ type: 'text', text: 'TST-1', marks: [{ type: 'code' }] }), data, { text: true }).changed).toBe(false)
    expect(convertMentionLinks({ type: 'doc', content: [{ type: 'codeBlock', content: [{ type: 'text', text: 'TST-1 /workspace/issue/TST-1' }] }] }, data, { text: true }).changed).toBe(false)
  })

  it('turns a GitHub pull request URL into the review it belongs to', () => {
    const converted = convertMentionLinks(doc(link('https://github.com/acme/web/pull/7', 'https://github.com/acme/web/pull/7')), data)
    expect(paragraph(converted.content)[0].attrs).toMatchObject({ mentionType: 'review', id: 'review-1', label: 'Fix login' })
    expect(convertMentionLinks(doc(link('https://github.com/acme/web/pull/8', 'https://github.com/acme/web/pull/8')), data).changed).toBe(false)
    // Any provider URL a held review carries (a GitLab merge request, a self-hosted instance) maps the same way.
    const gitlab = mentionFixture({ reviews: [{ ...data.reviews![0], provider: 'gitlab', url: 'https://gitlab.example.com/acme/web/-/merge_requests/12' }] as never })
    expect(paragraph(convertMentionLinks(doc(link('https://gitlab.example.com/acme/web/-/merge_requests/12/diffs?x=1', 'https://gitlab.example.com/acme/web/-/merge_requests/12')), gitlab).content)[0]?.type).toBe('text')
    expect(paragraph(convertMentionLinks(doc(link('https://gitlab.example.com/acme/web/-/merge_requests/12', 'https://gitlab.example.com/acme/web/-/merge_requests/12')), gitlab).content)[0].attrs).toMatchObject({ mentionType: 'review' })
  })

  it('keeps update and comment links as typed references', () => {
    const update = convertMentionLinks(doc(link('/workspace/project/project-one/overview#update-abc', '/workspace/project/project-one/overview#update-abc')), data)
    expect(paragraph(update.content)[0].attrs).toMatchObject({ mentionType: 'update', id: '/workspace/project/project-one/overview#update-abc' })
    const comment = convertMentionLinks(doc(link('/workspace/issue/TST-1/test-issue#comment-xyz', 'Comment')), data)
    expect(paragraph(comment.content)[0].attrs).toMatchObject({ mentionType: 'comment' })
  })
})

describe('plain text references', () => {
  const data = mentionFixture()

  it('converts bare Flow URLs, held and unheld team-key identifiers and @names, but not other prefixes', () => {
    const converted = convertMentionLinks(doc({ type: 'text', text: `Fix TST-1 and TST-77, see ${mentionUrls.document}. Ping @Teammate; UTF-8 and DEV-1 stay.` }), data, { text: true })
    const nodes = paragraph(converted.content)
    expect(nodes.filter(node => node.type === 'mention').map(node => `${node.attrs?.mentionType}:${node.attrs?.id}`)).toEqual(['issue:issue-1', 'issue:TST-77', 'document:document-1', 'user:user-2'])
    expect(nodes.map(node => node.text ?? '').join('')).toContain('UTF-8 and DEV-1 stay.')
    expect(converted.unresolved).toEqual([expect.objectContaining({ kind: 'issue', id: 'TST-77' })])
  })

  it('does nothing to text without the text option', () => {
    expect(convertMentionLinks(doc({ type: 'text', text: 'TST-1' }), data).changed).toBe(false)
  })

  it('reports issues and projects the client does not hold (paged workspaces) so they can be fetched by id', () => {
    const paged = mentionFixture({ issueCollectionPaged: true, issues: [], projects: [] })
    const converted = convertMentionLinks(doc(link('/workspace/issue/TST-5/title', 'the fix'), link('/workspace/project/gone/overview', 'Gone project')), paged)
    expect(converted.changed).toBe(false)
    expect(converted.unresolved.map(target => `${target.kind}:${target.id}`)).toEqual(['issue:TST-5', 'project:gone'])
    const bare = convertMentionLinks(doc(link('/workspace/issue/TST-5/title', '/workspace/issue/TST-5/title')), paged)
    expect(paragraph(bare.content)[0].attrs).toMatchObject({ mentionType: 'issue', id: 'TST-5', label: 'TST-5' })
  })

  it('types a team-key identifier into an issue target', () => {
    const data = mentionFixture({ issues: [makeIssue({ id: 'issue-9', identifier: 'TST-9', title: 'Nine' })] })
    expect(typedIdentifierTarget(data, 'tst-9')).toEqual({ kind: 'issue', id: 'issue-9', label: 'TST-9' })
    expect(typedIdentifierTarget(data, 'TST-10')).toEqual({ kind: 'issue', id: 'TST-10', label: 'TST-10' })
    expect(typedIdentifierTarget(data, 'DEV-1')).toBeUndefined()
  })

  it('maps every entity to attributes that resolve it again', () => {
    const data = mentionFixture()
    const issue = data.issues[0]
    expect(mentionAttrsForEntity(data, { kind: 'issue', issue })).toEqual({ mentionType: 'issue', id: 'issue-1', label: 'TST-1', title: 'Test issue', href: '/workspace/issue/TST-1/test-issue' })
    expect(linkMentionTarget(data, '/workspace/issue/TST-1/test-issue', '/workspace/issue/TST-1/test-issue')).toMatchObject({ kind: 'issue' })
  })
})
