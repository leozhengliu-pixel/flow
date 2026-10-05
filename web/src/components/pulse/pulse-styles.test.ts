import { describe, expect, it } from 'vitest'

// jsdom loads no stylesheets; read the rules themselves (Node APIs are untyped in this project).
async function readCss(path: string) {
  const fs = (await import(/* @vite-ignore */ `node:${'fs'}`)) as { readFileSync: (path: string, encoding: 'utf8') => string }
  const cwd = (globalThis as unknown as { process: { cwd: () => string } }).process.cwd()
  return fs.readFileSync(`${cwd}/${path}`, 'utf8')
}

function rule(css: string, selector: string) {
  const start = css.indexOf(`${selector}{`)
  expect(start, selector).toBeGreaterThanOrEqual(0)
  return css.slice(start + selector.length + 1, css.indexOf('}', start))
}

describe('Pulse comment composer styles', () => {
  it('keeps the shared editor inside the comment box (no 14px bleed) and puts the focus ring on the box', async () => {
    const css = await readCss('src/components/pulse/pulse-feed.css')
    const editor = rule(css, '.pulse-post-comment-editor>.pulse-post-comment-rich-editor')
    expect(editor).toContain('box-sizing:border-box')
    expect(editor).toContain('width:100%')
    expect(editor).toContain('margin:0')
    expect(rule(css, '.pulse-post-comment-editor:focus-within')).toContain('box-shadow:0 0 0 1px var(--theme-border-strong)')
    expect(rule(css, '.pulse-post-comment-editor')).toContain('box-sizing:border-box')
  })
})
