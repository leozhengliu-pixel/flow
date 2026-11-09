import { Paperclip, type LucideIcon } from 'lucide-react'

import { ToolbarGlyph, type ToolbarGlyphName } from './selection-toolbar-glyphs'

/**
 * Glyphs of the desktop document insert menu. Most are the selection toolbar's glyphs (16px viewBox, `currentColor`);
 * media, diagram and math have their own drawings here.
 */
const fromToolbar = (name: ToolbarGlyphName, scale = 1) => (({ size = 16 }: { size?: number | string }) => scale === 1
  ? <ToolbarGlyph name={name} size={Number(size)}/>
  : <span aria-hidden="true" style={{ display: 'inline-grid', width: Number(size), height: Number(size), placeItems: 'center' }}><ToolbarGlyph name={name} size={Number(size) * scale}/></span>) as unknown as LucideIcon

const MediaGlyph = ({ size = 16 }: { size?: number | string }) => <svg aria-hidden="true" focusable="false" width={size} height={size} viewBox="0 0 16 16" fill="currentColor">
  <path fillRule="evenodd" clipRule="evenodd" d="M4.5 1.5h7A3.5 3.5 0 0 1 15 5v6a3.5 3.5 0 0 1-3.5 3.5h-7A3.5 3.5 0 0 1 1 11V5a3.5 3.5 0 0 1 3.5-3.5ZM4.5 3A2 2 0 0 0 2.5 5v6a2 2 0 0 0 2 2h7a2 2 0 0 0 2-2V5a2 2 0 0 0-2-2h-7Z"/>
  <circle cx="8" cy="5.6" r="1.1"/>
  <path d="M2.5 11 5.2 8.3a.9.9 0 0 1 1.3 0L8.1 9.9l1.7-1.7a.9.9 0 0 1 1.3 0l2.4 2.4V11a2 2 0 0 1-2 2h-7a2 2 0 0 1-2-2Z"/>
</svg>

const DiagramGlyph = ({ size = 16 }: { size?: number | string }) => <svg aria-hidden="true" focusable="false" width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round">
  <rect x="9" y="1.9" width="6" height="2.7" rx="1" fill="currentColor" stroke="none"/>
  <rect x="1" y="11.4" width="6" height="2.7" rx="1" fill="currentColor" stroke="none"/>
  <path d="M10 4.7V6.2a1.5 1.5 0 0 1-1.5 1.5H5.25A1.75 1.75 0 0 0 3.5 9.45V11.2" strokeWidth="1.5"/>
</svg>

const MathGlyph = ({ size = 16 }: { size?: number | string }) => <svg aria-hidden="true" focusable="false" width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.6">
  <path d="M1.2 8.6 2.9 7.6 5 13.2 7.6 2.6H10.2"/>
  <path d="M8.8 7.2 14.4 13.4M14.4 7.2 8.8 13.4"/>
</svg>

export const SLASH_MENU_GLYPHS = {
  heading1: fromToolbar('heading1'),
  heading2: fromToolbar('heading2'),
  heading3: fromToolbar('heading3'),
  bulletList: fromToolbar('bulletList'),
  numberedList: fromToolbar('numberedList'),
  checklist: fromToolbar('checklist'),
  codeBlock: fromToolbar('codeBlock'),
  quote: fromToolbar('quote'),
  collapse: fromToolbar('collapse', .7),
  attach: (({ size = 16 }: { size?: number | string }) => <Paperclip size={Number(size) * .875}/>) as unknown as LucideIcon,
  media: MediaGlyph as unknown as LucideIcon,
  diagram: DiagramGlyph as unknown as LucideIcon,
  math: MathGlyph as unknown as LucideIcon,
}
