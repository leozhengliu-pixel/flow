import type { CSSProperties } from 'react'
import { NodeViewContent, NodeViewWrapper, type ReactNodeViewProps } from '@tiptap/react'
import { ViewGlyph, ViewIconPicker } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { calloutPickerColor, isCalloutColorName, normalizeCalloutColor } from './callout-model'
import './callout.css'

function Lightbulb() {
  return <svg aria-hidden="true" fill="currentColor" height="16" viewBox="0 0 16 16" width="16"><path d="M10 13a2 2 0 1 1-4 0zM7.989 1a5.033 5.033 0 0 1 3.737 8.404l-1.25 1.387A1.8 1.8 0 0 0 10.011 12H6.014a1.9 1.9 0 0 0-.475-1.252L4.252 9.293A4.988 4.988 0 0 1 7.988 1"/></svg>
}

/** The callout box: its icon button is the shared icon picker (icons, emojis, preset and custom colours). */
export function CalloutView({ editor, node, updateAttributes }: ReactNodeViewProps) {
  const { t } = useI18n()
  const color = normalizeCalloutColor(node.attrs.color)
  const icon = typeof node.attrs.icon === 'string' ? node.attrs.icon : ''
  const legacy = isCalloutColorName(color)
  const glyph = icon ? <ViewGlyph color="var(--callout-color)" icon={icon}/> : <Lightbulb/>
  return (
    <NodeViewWrapper as="aside" className={`description-callout${legacy ? ` is-${color}` : ''}`} data-callout="" data-color={color} data-icon={icon || undefined} style={legacy ? undefined : { '--callout-color': color } as CSSProperties}>
      <span className="description-callout-controls" contentEditable={false} data-i18n-ignore>
        {editor.isEditable
          ? <ViewIconPicker ariaLabel={t('Change callout icon or color')} color={calloutPickerColor(color)} icon={icon} onChange={next => updateAttributes({ icon: next.icon, color: normalizeCalloutColor(next.color) })} triggerContent={glyph}/>
          : <span className="description-callout-glyph">{glyph}</span>}
      </span>
      <NodeViewContent className="description-callout-content"/>
    </NodeViewWrapper>
  )
}
