import { describe, expect, it } from 'vitest'

import { highlightCode } from './code-block/code-block-highlight'

const fs = await import('node:' + 'fs') as { readFileSync: (path: string, encoding: 'utf8') => string }
const read = (relative: string) => fs.readFileSync(decodeURIComponent(new URL(relative, import.meta.url).pathname), 'utf8')
const bodyCss = read('rich-text-body.css')
const tokens = read('../../styles/tokens.css')

const rules = bodyCss.replace(/\/\*[\s\S]*?\*\//g, '').split('}').map(rule => rule.trim()).filter(Boolean).map(rule => {
  const [selector, ...declarations] = rule.split('{')
  return { selector: selector.trim(), body: declarations.join('{') }
})

describe('rich-text body parity styles', () => {
  it('is loaded by every surface that renders a body', () => {
    for (const file of ['../issue/issue-description-editor.tsx', '../project-detail/project-detail-page.tsx']) {
      expect(read(file), file).toContain('rich-text-body.css')
    }
  })

  it('uses design tokens only: no colour literals', () => {
    expect(bodyCss).not.toMatch(/#[\da-f]{3,8}\b|\b(?:rgb|rgba|hsl|hsla|lch|oklch)\(/i)
  })

  it('defines every --rt-* token it reads, with dark values at :root', () => {
    const used = new Set([...bodyCss.matchAll(/var\((--rt-[\w-]+)/g)].map(match => match[1]))
    expect(used.size).toBeGreaterThan(10)
    for (const token of used) expect(tokens, token).toMatch(new RegExp(`:root\\{[^}]*${token}:`))
  })

  it('writes every colour for the default theme only, so light keeps its existing look', () => {
    const colourRules = rules.filter(rule => /var\(--(?:rt-|text|bright|muted|bg-|theme-)/.test(rule.body) && /(?:^|[\s;])(?:color|background|border-[a-z-]*color|border-color|box-shadow)\s*:/.test(rule.body))
    expect(colourRules.length).toBeGreaterThan(10)
    for (const rule of colourRules) {
      const shared = rule.selector.startsWith(':root :is(') && !/data-theme/.test(rule.selector)
      if (!shared) continue
      // Shared (theme independent) rules may only carry geometry, type metrics and the heading colour that already existed.
      expect(rule.body, rule.selector.slice(0, 120)).not.toMatch(/var\(--rt-/)
    }
  })

  it('restores the light marker colour for the custom list glyphs', () => {
    expect(bodyCss).toMatch(/:root\[data-theme="light"\][^{]*\{\s*color: var\(--theme-text-secondary\);/)
  })

  it('draws bullets and numbers as hanging glyphs 24px left of the text, one glyph per nesting level', () => {
    expect(bodyCss).toContain('content: "\\2022"')
    expect(bodyCss).toContain('content: "\\25E6"')
    expect(bodyCss).toContain('left: calc(-24px + .25em)')
    expect(bodyCss).toContain('content: counter(list-item) ". "')
    expect(bodyCss).toContain('content: counter(list-item, lower-alpha) ". "')
    expect(bodyCss).toContain('content: counter(list-item, lower-roman) ". "')
  })

  it('hangs a 14px checkbox 24px left of the text and dims only the checked item itself', () => {
    expect(bodyCss).toMatch(/ul\[data-type="taskList"\] \{ padding-left: 24px; \}/)
    expect(bodyCss).toMatch(/> label \{\s*left: -24px;\s*top: 5px;\s*width: 14px;\s*height: 14px;/)
    expect(bodyCss).toMatch(/> div > p:first-child \{ opacity: \.65; \}/)
  })

  it('keeps comment bodies, update bodies and agent answers on the same rules as documents and issues', () => {
    const selector = rules.find(rule => rule.body.includes('letter-spacing: -.00666667em'))?.selector ?? ''
    for (const surface of ['.document-editor .description-editor', '.project-overview__description-editor', '.project-activity__rich .ProseMirror', '.project-overview__latest-update-body .ProseMirror', '.comment-body .ProseMirror', '.comment-prosemirror', '.project-activity__comment-body .ProseMirror', 'messageDocument']) {
      expect(selector, surface).toContain(surface)
    }
  })

  it('does not force the compact sub-issue and dialog editors to 15px', () => {
    const metrics = rules.find(rule => rule.body.includes('font-size: 15px;') && rule.body.includes('line-height: 24px;'))
    expect(metrics?.selector).toBe(':root :is(.project-activity__rich .ProseMirror, .project-overview__latest-update-body .ProseMirror)')
  })
})

describe('code block control keywords', () => {
  it('marks control-flow keywords so a theme can colour them apart from declarations', () => {
    const code = 'const a = 1\nif (a) return a'
    const ranges = highlightCode(code, 'js')
    const slice = (name: string) => ranges.filter(range => range.className.includes(name)).map(range => code.slice(range.from, range.to))
    expect(slice('hljs-control')).toEqual(expect.arrayContaining(['if', 'return']))
    expect(slice('hljs-control')).not.toContain('const')
    expect(slice('hljs-keyword')).toEqual(expect.arrayContaining(['const', 'if', 'return']))
  })
})

describe('issue layout at narrow widths', () => {
  it('lays the issue page out on a 914px column: 579px body, 15px gap, 320px properties', () => {
    expect(tokens).toContain('@media(max-width:1240px) and (min-width:801px){.issue-layout{width:calc(100% - 58px);grid-template-columns:minmax(0,1fr) 320px;gap:15px}.issue-document{padding-right:14px}}')
  })
})
