import { describe, expect, it } from 'vitest'
import { markdownPlainText, markdownSnippet } from './markdown-plain-text'

describe('markdownPlainText', () => {
  it('keeps a link\'s text and drops its target, including mention links', () => {
    expect(markdownPlainText('See [DEV-1](/ws/issue/DEV-1/title) and [Launch plan](/ws/document/plan-abc) now')).toBe('See DEV-1 and Launch plan now')
    expect(markdownPlainText('[a \\[b\\] c](/x/y_(z)) tail')).toBe('a [b] c tail')
    expect(markdownPlainText('[Title](<https://x.test/a b> "tip") and <https://x.test>')).toBe('Title and https://x.test')
  })

  it('writes people as @name and images as their alt text', () => {
    expect(markdownPlainText('Ping @Viewer about ![diagram](https://x.test/a.png)')).toBe('Ping @Viewer about diagram')
  })

  it('reads agent entity shortcodes as their label', () => {
    expect(markdownPlainText('Fix [agentEntity kind="issue" id="issue-1" label="DEV-1"] soon [agentEntity kind="document" id="d" label="Launch \\"plan\\""]')).toBe('Fix DEV-1 soon Launch "plan"')
  })

  it('strips emphasis, headings, quotes, list markers, tasks and code fences', () => {
    const markdown = '# Title\n\n> quoted **bold** and _em_ and ~~gone~~ `code`\n\n- [ ] first\n- [x] second\n1. third\n\n```ts\nconst a = 1\n```\n'
    expect(markdownPlainText(markdown)).toBe('Title quoted bold and em and gone code first second third const a = 1')
  })

  it('does not eat snake_case words or lone asterisks', () => {
    expect(markdownPlainText('use snake_case_name and 2 * 3')).toBe('use snake_case_name and 2 * 3')
  })

  it('collapses whitespace and handles empty input', () => {
    expect(markdownPlainText('  a\n\n\n b\t c ')).toBe('a b c')
    expect(markdownPlainText(undefined)).toBe('')
    expect(markdownPlainText('')).toBe('')
  })

  it('never leaves link syntax in the output', () => {
    expect(markdownPlainText('x [a](/p) y ![b](/q.png) [c][1]\n\n[1]: https://x.test')).not.toContain('](')
  })

  it('cuts a snippet with an ellipsis', () => {
    expect(markdownSnippet('[abcdef](/x) ghijkl', 8)).toBe('abcdef…')
    expect(markdownSnippet('short', 8)).toBe('short')
  })
})
