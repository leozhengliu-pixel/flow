import { readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { EMBED_LAYOUTS, embedLayoutFor } from './embed-providers'

const css = readFileSync(path.join(import.meta.dirname, 'embed.module.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')

/** The declarations of the first rule whose selector list contains `selector`. */
function rule(selector: string) {
  const match = css.split('}').map(chunk => chunk.trim()).find(chunk => chunk.split('{')[0].split(',').some(part => part.trim() === selector))
  if (!match) throw new Error(`no rule for ${selector}`)
  return match.split('{')[1]
}

describe('embed layout (measured on Linear)', () => {
  it('spans the content column on every surface instead of capping at 640px', () => {
    const embed = rule('.embed')
    expect(embed).toContain('width:100%')
    expect(embed).not.toMatch(/max-width/)
    expect(embed).toContain('border-radius:6px')
    expect(css).not.toMatch(/[{;]max-width:\s*640px/)
    expect(css).not.toMatch(/\.cardWrap\{max-width/)
  })

  it('uses 5:3 for video players and 4:3 for boards', () => {
    expect(rule('.video')).toContain('aspect-ratio:5/3')
    expect(rule('.tall')).toContain('aspect-ratio:4/3')
    for (const provider of ['youtube', 'loom', 'descript', 'tella'] as const) expect(EMBED_LAYOUTS[provider]).toMatchObject({ kind: 'iframe', aspect: 'video' })
    expect(EMBED_LAYOUTS.miro).toMatchObject({ kind: 'iframe', aspect: 'tall' })
    expect(Object.values(EMBED_LAYOUTS).some(layout => layout.height)).toBe(false)
  })

  it('draws Figma and X posts as link cards and repository files as file cards', () => {
    expect(embedLayoutFor('figma').kind).toBe('link')
    expect(embedLayoutFor('twitter').kind).toBe('link')
    expect(embedLayoutFor('github').kind).toBe('card')
    expect(embedLayoutFor('gitlab').kind).toBe('card')
    // Unknown providers from newer content fall back to a video-shaped player.
    expect(embedLayoutFor('something-new')).toMatchObject({ kind: 'iframe', aspect: 'video' })
  })

  it('sizes the link card as Linear does: 100px row, 16px / 20px text padding, 100px-high image, hover surface', () => {
    const card = rule('.embed a.linkCard.linkCard.linkCard')
    expect(card).toContain('height:100px')
    expect(card).toContain('display:flex')
    expect(card).toContain('text-decoration:none')
    expect(rule('.linkText')).toContain('padding:12px 20px 12px 16px')
    expect(rule('.linkText')).toContain('gap:6px')
    expect(rule('.linkTitle')).toContain('font:500 13px')
    expect(rule('.linkDescription')).toContain('-webkit-line-clamp:2')
    expect(rule('.linkDescription')).toContain('white-space:pre-wrap')
    expect(rule('.linkImage')).toContain('height:100%')
    expect(rule('.linkImage')).toContain('object-fit:contain')
    expect(rule('.linkWrap:hover')).toContain('var(--theme-surface-hover)')
  })

  it('styles the paste popover as a 10px-radius menu of 34px rows with 4px-inset highlights', () => {
    const popover = rule('.popover')
    expect(popover).toContain('position:fixed')
    expect(popover).toContain('border-radius:10px')
    expect(popover).toContain('padding:4px 0')
    expect(rule('.popoverItem')).toContain('height:34px')
    expect(rule('.popoverItem')).toContain('padding:0 14px')
    expect(rule('.popoverItem::before')).toContain('inset:0 4px')
    expect(rule('.popoverItem::before')).toContain('border-radius:6px')
    expect(rule('.popoverItem[data-active="true"]::before')).toContain('var(--theme-surface-hover)')
    expect(rule('.popover[data-open="true"]')).toContain('opacity:1')
  })
})
