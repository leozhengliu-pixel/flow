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
    expect(used.size).toBeGreaterThan(5)
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

  it('gives the custom list glyphs the text colour in the light theme (the reference markers are not dimmed)', () => {
    expect(bodyCss).toMatch(/:root\[data-theme="light"\][^{]*li::before \{\s*color: inherit;/)
  })

  it('carries its own light theme rules built from the --rt-* light tokens and the body text colour', () => {
    const light = rules.filter(rule => rule.selector.startsWith(':root[data-theme="light"] :is('))
    expect(light.length).toBeGreaterThan(10)
    const css = light.map(rule => `${rule.selector}{${rule.body}`).join('\n')
    for (const token of ['--rt-link', '--rt-inline-code-bg', '--rt-inline-code-border', '--rt-rule', '--rt-code-border', '--rt-chip-bg', '--rt-hairline']) expect(css, token).toContain(`var(${token})`)
    // The light surfaces read --text (#2f2f31), not --bright (#1b1b1b), for body copy.
    expect(css).toContain('color: var(--text)')
    // Only headings are brighter than the body (#1b1b1b against #2f2f31), as in the reference light theme.
    expect(css.match(/var\(--bright\)/g)).toHaveLength(1)
    expect(css).toMatch(/:is\(h1, h2, h3, h4\)\{ color: var\(--bright\);/)
    for (const token of ['--rt-link', '--rt-inline-code-bg', '--rt-rule', '--rt-chip-bg']) expect(tokens, token).toMatch(new RegExp(`:root\\[data-theme="light"\\]\\{[^}]*${token}:`))
  })

  it('sets the lightbox from the reference app: solid page, faint 8px checkerboard, 80% caption', () => {
    const lightbox = read('lightbox-editor.css')
    expect(tokens).toContain(':root{--lightbox-bg:lch(5.52% .4 272);--lightbox-checker:lch(90.451% 1.2 272 / .08);--lightbox-caption-bg:lch(5.52% .4 272 / .8)}')
    expect(tokens).toContain(':root[data-theme="light"]{--lightbox-bg:lch(97.94% .5 282);--lightbox-checker:lch(19.588% 1.25 282 / .08);--lightbox-caption-bg:lch(97.94% .5 282 / .8)}')
    expect(lightbox).toContain('repeating-conic-gradient(var(--lightbox-checker) 0% 25%, transparent 0% 50%) 0 0 / 8px 8px')
    expect(lightbox).toMatch(/\.flow-lightbox-zoom \{[^}]*min-width: 56px;/)
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

/** The class name of every highlighted token, keyed by its text (the last one wins for repeated words). */
function classes(code: string, language: string) {
  const byText = new Map<string, string>()
  for (const range of highlightCode(code, language)) byText.set(code.slice(range.from, range.to), range.className)
  return byText
}

describe('code block syntax scopes', () => {
  it('tells variables, calls, types and constants apart in C-like languages', () => {
    const tokens = classes('const url = `/api/${id}`\nasync function load<T>(opts: Options): Promise<T> {\n  return fetch(url).then(r => r.json(opts.retries))\n}', 'ts')
    expect(tokens.get('url')).toBe('hljs-constant')
    expect(tokens.get('fetch')).toContain('hljs-title')
    expect(tokens.get('load')).toBe('hljs-call')
    expect(tokens.get('Options')).toContain('class_')
    expect(tokens.get('retries')).toBe('hljs-property')
    expect(tokens.get('id')).toBe('hljs-ident')
    expect(tokens.get('return')).toBe('hljs-keyword hljs-control')
    expect(tokens.get('async')).toBe('hljs-keyword')
  })

  it('treats operator words as plain text and the module keywords as control flow', () => {
    const tokens = classes("import a from 'x'\nif (b instanceof C) { delete a.b }", 'js')
    expect(tokens.get('import')).toBe('hljs-keyword hljs-control')
    expect(tokens.get('from')).toBe('hljs-keyword hljs-control')
    expect(tokens.get('instanceof')).toBe('hljs-plain')
    expect(tokens.get('delete')).toBe('hljs-plain')
  })

  it('splits a regexp into plain delimiters and a coloured pattern', () => {
    const code = 'const re = /ab+c/gi'
    const ranges = highlightCode(code, 'js').filter(range => range.className.includes('hljs-regexp'))
    expect(ranges.map(range => code.slice(range.from, range.to))).toEqual(['ab+c', 'gi'])
  })

  it('leaves the Go package name plain and colours calls and types', () => {
    const tokens = classes('package main\ntype Server struct{}\nfunc (s *Server) Start() error { return fmt.Errorf("x") }', 'go')
    expect(tokens.get('main')).toBe('hljs-plain')
    expect(tokens.get('Server')).toBe('hljs-ident-type')
    expect(tokens.get('Start')).toBe('hljs-call')
    expect(tokens.get('Errorf')).toBe('hljs-call')
    expect(tokens.get('s')).toBe('hljs-ident')
  })

  it('tells JSX components from intrinsic tags and keeps brace expressions out of the string colour', () => {
    const tokens = classes('const a = <Layout title="x"><div className="a" onClick={() => go(true)}>{items.map(i => i)}</div></Layout>', 'tsx')
    expect(tokens.get('Layout')).toBe('hljs-ident-type')
    expect(tokens.get('div')).toBe('hljs-ident')
    expect(tokens.get('"x"')).toBe('hljs-string')
    expect(tokens.get('className')).not.toBe('hljs-string')
  })

  it('colours qualified Go types as types and the package and values as plain or variables', () => {
    const tokens = classes('func (s *Server) Serve(ctx context.Context, r *http.Request, w http.ResponseWriter) error {\n\tvar t time.Duration = time.Second\n\treturn nil\n}', 'go')
    expect(tokens.get('Context')).toBe('hljs-ident-type')
    expect(tokens.get('Request')).toBe('hljs-ident-type')
    expect(tokens.get('ResponseWriter')).toBe('hljs-ident-type')
    expect(tokens.get('Duration')).toBe('hljs-ident-type')
    expect(tokens.get('context')).toBe('hljs-plain')
    expect(tokens.get('Second')).not.toBe('hljs-ident-type')
  })

  it('colours shell commands as calls, assignments as variables and keeps the sigil plain', () => {
    const tokens = classes('export FOO="bar"\nif [ -f $FILE ]; then npm run build; fi', 'bash')
    expect(tokens.get('export')).toBe('hljs-keyword hljs-control')
    expect(tokens.get('FOO')).toBe('hljs-ident')
    expect(tokens.get('npm')).toBe('hljs-call')
    expect(tokens.get('then')).toBe('hljs-keyword')
    expect(tokens.get('FILE')).toBe('hljs-variable')
    expect(tokens.has('$FILE')).toBe(false)
    expect(tokens.get('#!/bin/sh')).toBeUndefined()
    expect(classes('#!/bin/sh\necho hi', 'bash').get('#!/bin/sh')).toBe('hljs-comment')
  })

  it('colours JSON keys like strings and keeps CSS urls and attribute selectors plain', () => {
    expect(classes('{"a": 1, "ok": true}', 'json').get('"a"')).toBe('hljs-string')
    expect(classes('{"a": 1, "ok": true}', 'json').get('true')).toBe('hljs-literal')
    expect(classes('a { background: url(x.png) }', 'css').get('x.png')).toBe('hljs-plain')
    expect(classes('<a href="/x">y</a>', 'xml').get('/x')).toBe('hljs-string')
  })

  it('treats python decorators as calls and keeps built-in calls apart from annotations', () => {
    const tokens = classes('@cache\ndef f(x: int):\n    return len(x)', 'python')
    expect(tokens.get('@cache')).toBe('hljs-call')
    expect(tokens.get('int')).toBe('hljs-built_in')
    expect(tokens.get('len')).toBe('hljs-built_in hljs-call')
  })

  it('maps every scope onto the --syntax palette, with dark and light values and no literals', () => {
    const css = read('code-block/code-block.css')
    const palette = ['control', 'keyword', 'type', 'function', 'variable', 'constant', 'string', 'regexp', 'comment', 'line-number']
    for (const name of palette) {
      expect(tokens, name).toMatch(new RegExp(`:root\\{[^}]*--syntax-${name}:#[\\da-f]{6}`))
      expect(tokens, name).toMatch(new RegExp(`:root\\[data-theme="light"\\]\\{[^}]*--syntax-${name}:#[\\da-f]{6}`))
    }
    const syntax = css.slice(css.indexOf('/* Syntax highlighting'), css.indexOf('/* Popovers'))
    expect(syntax).not.toMatch(/#[\da-f]{3,8}\b|\b(?:rgb|lch|hsl)a?\(/i)
    // The same rule decides the colour of merged scopes (decorations share one element), so comments are never italic.
    expect(syntax).not.toMatch(/\.hljs-comment[^}]*italic/)
  })

  it('sets the code font metrics: 12px / 20px blocks and 0.9375em inline code on the system monospace stack', () => {
    const block = read('code-block/code-block.css')
    expect(block).toMatch(/\.flow-code-block \{[^}]*font-size: 12px;\s*line-height: 20px;[^}]*letter-spacing: -\.002em/)
    expect(bodyCss).toMatch(/font-size: \.9375em;\s*letter-spacing: calc\(-\.1px - \.002em\)/)
    expect(read('../../styles/foundations.css')).toMatch(/--font-monospace: ui-monospace, "SFMono-Regular", Menlo/)
    expect(read('../../styles/foundations.css')).not.toMatch(/Berkeley/)
  })
})

describe('embedded surface parity (floating chat panel, thread cards, reactions, images, hover cards)', () => {
  const flat = (css: string) => css.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\s+/g, ' ')

  it('anchors the floating chat panel 32px from the right and 34px above the bottom, 80% of the viewport tall and 400px wide', () => {
    for (const file of ['../agent/agent-panel.module.css', '../agent/agent-chat-panel.module.css']) {
      const css = flat(read(file))
      expect(css, file).toMatch(/bottom: ?34px/)
      expect(css, file).toMatch(/right: ?32px/)
      expect(css, file).toMatch(/width: ?400px/)
      expect(css, file).toMatch(/height: ?80vh/)
      expect(css, file).toMatch(/height: ?round\(down, ?80vh, ?1px\)/)
      expect(css, file).not.toMatch(/554px|563px|top: ?105px|top: ?107px/)
    }
  })

  it('switches between gutter cards and the popover at the same viewport width in CSS and in the component', () => {
    const css = flat(read('../documents/inline-comments/document-inline-comments.css'))
    const source = read('../documents/inline-comments/document-inline-comments.tsx')
    const width = source.match(/GUTTER_MIN_VIEWPORT = (\d+)/)?.[1]
    expect(width).toBe('1232')
    expect(css).toContain(`@media (min-width: ${width}px)`)
    // Cards sit 2px right of the 833px editor; it keeps 34px from the sidebar, is centred on wide pages and gives up width before overlapping the gutter.
    expect(css).toContain('left: calc(100% + 16px)')
    expect(css).toContain('max(33.5px')
    expect(css).toContain('calc(100% - 1127.5px)')
    expect(css).toContain('calc(100% - var(--document-shift) - 294.5px)')
    // Cards fill the rest of the page, 10px short of its right edge, from 284px up to 372px.
    expect(css).toContain('width: min(372px, calc(100cqw - var(--document-shift) - var(--document-canvas-width) - 10px))')
    expect(css).toMatch(/\.document-inline-gutter\.is-popover \.document-thread-card \{ width: 360px;/)
  })

  it('draws thread cards with a 12px padding, an 18px header, a flush 15px body and a one-row reply', () => {
    const css = flat(read('../documents/inline-comments/document-inline-comments.css'))
    expect(css).toMatch(/\.document-thread-card \{[^}]*padding: 12px;[^}]*border: \.5px solid var\(--document-comment-card-border\);[^}]*border-radius: 10px/)
    expect(css).toMatch(/\.document-thread-comment > header \{[^}]*height: 18px;/)
    expect(css).toMatch(/\.document-thread-avatar \{ width: 18px; height: 18px;/)
    expect(css).toMatch(/\.document-thread-icon \{ width: 28px; height: 28px;/)
    expect(css).toMatch(/\.document-thread-body \{ margin-top: 6px;[^}]*font-size: 15px;[^}]*line-height: 24px;/)
    expect(css).toMatch(/\.document-thread-send \{ width: 24px; height: 24px;/)
    for (const token of ['--document-comment-card-bg', '--document-comment-card-border', '--document-popover-bg', '--document-popover-border', '--document-send-bg']) {
      expect(tokens, token).toMatch(new RegExp(`:root\\{[^}]*${token}:`))
      expect(tokens, token).toMatch(new RegExp(`:root\\[data-theme="light"\\]\\{[^}]*${token}:`))
    }
  })

  it('draws reaction pills 28px tall with the viewer\'s own reaction tinted, in both themes', () => {
    const css = flat(tokens)
    expect(css).toMatch(/\.reaction-pills button\{[^}]*height:28px;[^}]*padding:0 7px;[^}]*border-radius:9999px;/)
    expect(css).toMatch(/\.reaction-pills button\[aria-pressed=true\]\{[^}]*background:var\(--reaction-active-bg\)/)
    expect(css).toContain('--reaction-active-bg:lch(19.238% 16.454 284.983)')
    expect(css).toContain('--reaction-active-bg:#e7e8f2')
  })

  it('lays an image block out 22px from its neighbours with a 14px-icon toolbar split by a hairline', () => {
    const css = flat(read('../issue/issue-description-editor.css'))
    expect(css).toMatch(/\.description-image-node \{ width: min\(785px, calc\(100% \+ 16px\)\); margin: 22px 0; \}/)
    expect(css).toMatch(/\.description-image-toolbar \{[^}]*top: 10px;[^}]*right: 10px;/)
    expect(css).toMatch(/\.description-image-toolbar svg \{ width: 14px; height: 14px; \}/)
    expect(css).toContain('.description-image-toolbar button:nth-child(5)::before')
    expect(css).toMatch(/outline: 2px solid var\(--description-selected-outline\);\s*outline-offset: 0;/)
  })

  it('shows the lightbox on a solid page with a zoom label, copy / link actions and the caption', () => {
    const css = flat(read('lightbox-editor.css'))
    expect(css).toContain('background: var(--lightbox-bg)')
    for (const selector of ['.flow-lightbox-bar', '.flow-lightbox-zoom', '.flow-lightbox-actions', '.flow-lightbox-separator', '.flow-lightbox-caption']) expect(css, selector).toContain(selector)
    const provider = read('lightbox-editor-provider.tsx')
    for (const label of ['Download', 'Copy image', 'Copy link', 'Close']) expect(provider).toContain(`aria-label="${label}"`)
  })

  it('gives hover cards the popover surface, a 34px title gap and the hairline rule of the reference', () => {
    const css = flat(read('../agent/agent-entity-hover.module.css'))
    expect(css).toContain('border:.5px solid var(--hover-card-border)')
    expect(css).toContain('background:var(--hover-card-bg)')
    expect(css).toContain('box-shadow:var(--hover-card-shadow)')
    expect(css).toContain('.rule{height:0;margin:10px 0 10.5px;')
    expect(read('../agent/agent-entity-hover.tsx')).toContain('side="top" sideOffset={3}')
    expect(tokens).toContain('--hover-card-bg:lch(12.72% .85 272)')
  })

  it('keeps an assistant answer as wide as the column so its code blocks span it', () => {
    expect(flat(read('../agent/agent-page.module.css'))).toMatch(/\.assistant \.messageContent \{ width: 100%; max-width: 100%; \}/)
  })
})

describe('issue layout at narrow widths', () => {
  it('lays the issue page out on a 914px column: 579px body, 15px gap, 320px properties', () => {
    expect(tokens).toContain('@media(max-width:1240px) and (min-width:801px){.issue-layout{width:calc(100% - 58px);grid-template-columns:minmax(0,1fr) 320px;gap:15px}.issue-document{padding-right:14px}}')
  })
})
