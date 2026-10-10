import { Node, mergeAttributes } from '@tiptap/core'
import { ReactNodeViewRenderer } from '@tiptap/react'
import { CalloutView } from '@/components/editor/callout/callout-node-view'
import { isCalloutColorName, normalizeCalloutColor, parseCalloutAttrs, serializeCalloutAttrs } from '@/components/editor/callout/callout-model'
import '@/components/editor/callout/callout.css'

export { CALLOUT_ALERT_PRESETS, calloutColors } from '@/components/editor/callout/callout-model'
export type { CalloutColor } from '@/components/editor/callout/callout-model'

/**
 * The callout's schema, Markdown syntax and HTML: `:::callout{cyan}` (old form) or `:::callout{#ff8800 icon="Bell"}`.
 * `color` is a legacy name (cyan|gray|green|yellow|orange|red|purple) or `#rrggbb`; `icon` is '' (lightbulb), an
 * icon-picker icon name or an emoji. The headless editor uses this; the live editors use DescriptionCallout.
 */
export const DescriptionCalloutSchema = Node.create({
  name: 'callout',
  group: 'block',
  content: 'block+',
  defining: true,
  addAttributes() {
    return { color: { default: 'cyan' }, icon: { default: '' } }
  },
  parseHTML() {
    return [{
      tag: 'aside[data-callout]',
      getAttrs: element => {
        const aside = element as HTMLElement
        return { color: normalizeCalloutColor(aside.dataset.color), icon: aside.dataset.icon ?? '' }
      },
    }]
  },
  renderHTML({ HTMLAttributes }) {
    const color = normalizeCalloutColor(HTMLAttributes.color)
    const icon = typeof HTMLAttributes.icon === 'string' ? HTMLAttributes.icon : ''
    const { color: _color, icon: _icon, ...rest } = HTMLAttributes
    return ['aside', mergeAttributes(rest, {
      'data-callout': '',
      'data-color': color,
      ...(icon ? { 'data-icon': icon } : {}),
      class: `description-callout${isCalloutColorName(color) ? ` is-${color}` : ''}`,
      ...(isCalloutColorName(color) ? {} : { style: `--callout-color:${color}` }),
    }), 0]
  },
  markdownTokenName: 'descriptionCallout',
  parseMarkdown: (token, helpers) => {
    const { color, icon } = token as unknown as { color?: string; icon?: string }
    const children = helpers.parseChildren(token.tokens ?? [])
    return helpers.createNode('callout', { color: normalizeCalloutColor(color), icon: icon ?? '' }, children.length ? children : [helpers.createNode('paragraph', {}, [])])
  },
  renderMarkdown: (node, helpers) => `:::callout{${serializeCalloutAttrs(node.attrs?.color, node.attrs?.icon)}}\n${helpers.renderChildren(node.content ?? [], '\n\n').replace(/\n+$/, '')}\n:::\n`,
  markdownTokenizer: {
    name: 'descriptionCallout',
    level: 'block' as const,
    start: (src: string) => src.indexOf(':::callout'),
    tokenize(src, _tokens, lexer) {
      const match = src.match(/^:::callout(?:\{([^}\n]*)\})?[ \t]*\n(?:([\s\S]*?)\n)?:::[ \t]*(?:\n|$)/)
      if (!match) return undefined
      const { color, icon } = parseCalloutAttrs(match[1])
      const body = match[2] ?? ''
      return { type: 'descriptionCallout', raw: match[0], color, icon, text: body, tokens: lexer.blockTokens(body) }
    },
  },
})

/** The callout with its icon-picker view, for the React editors. */
export const DescriptionCallout = DescriptionCalloutSchema.extend({
  addNodeView() {
    return ReactNodeViewRenderer(CalloutView)
  },
})
