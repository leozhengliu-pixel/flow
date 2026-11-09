import type { LucideIcon } from 'lucide-react'
import { createElement } from 'react'

import { AttachSlashGlyph, DiagramSlashGlyph, MathSlashGlyph, MediaSlashGlyph, ToolbarSlashGlyph } from './slash-menu-glyphs'
import type { ToolbarGlyphName } from './selection-toolbar-glyphs'

const toolbar = (name: ToolbarGlyphName, scale = 1) => (({ size }: { size?: number | string }) => createElement(ToolbarSlashGlyph, { name, scale, size })) as unknown as LucideIcon

/** Icons of the desktop document insert menu, keyed like the lucide icons they replace. */
export const SLASH_MENU_GLYPHS = {
  heading1: toolbar('heading1'),
  heading2: toolbar('heading2'),
  heading3: toolbar('heading3'),
  bulletList: toolbar('bulletList'),
  numberedList: toolbar('numberedList'),
  checklist: toolbar('checklist'),
  codeBlock: toolbar('codeBlock'),
  quote: toolbar('quote'),
  collapse: toolbar('collapse', .7),
  attach: AttachSlashGlyph as unknown as LucideIcon,
  media: MediaSlashGlyph as unknown as LucideIcon,
  diagram: DiagramSlashGlyph as unknown as LucideIcon,
  math: MathSlashGlyph as unknown as LucideIcon,
}
