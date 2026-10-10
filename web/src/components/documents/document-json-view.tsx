/**
 * Read-only rendering of a ProseMirror JSON document as plain React (no editor instance), with the document page's
 * typography. Used by the history dialog; also fine for any other static preview of a document (deleted state, ...).
 * Unknown nodes degrade to their children / text, so a version written by a newer editor never breaks the view.
 */
import { cloneElement, Fragment, isValidElement, type CSSProperties, type ReactElement, type ReactNode } from 'react'
import { DIFF_ADDED_MARK, DIFF_REMOVED_MARK, nodeText, type PMMark, type PMNode } from './document-history-diff'
import { embedLayoutFor } from '@/components/editor/embeds/embed-providers'
import { ReadOnlyEmbed } from '@/components/editor/embeds/embed-node-view'
import { isEmptyDocument } from './document-json-doc'
import './document-json-view.css'

const SAFE_URL = /^(https?:|mailto:|tel:|\/|#)/i
const safeHref = (value: unknown) => (typeof value === 'string' && SAFE_URL.test(value.trim()) ? value.trim() : undefined)

function diffAttrs(node: PMNode) {
  const kind = node.attrs?.__diff
  const change = node.attrs?.__change
  if (kind !== 'added' && kind !== 'removed') return {}
  return { className: `is-diff-${kind}`, 'data-change-id': typeof change === 'number' ? change : undefined, 'data-diff': kind }
}

function renderMarks(node: PMNode, base: ReactNode, key: string): ReactNode {
  let out = base
  for (const mark of node.marks ?? []) out = applyMark(mark, out, key)
  return out
}

function applyMark(mark: PMMark, child: ReactNode, key: string): ReactNode {
  switch (mark.type) {
    case 'bold': return <strong key={key}>{child}</strong>
    case 'italic': return <em key={key}>{child}</em>
    case 'strike': return <s key={key}>{child}</s>
    case 'underline': return <u key={key}>{child}</u>
    case 'code': return <code key={key}>{child}</code>
    case 'highlight': return <mark key={key}>{child}</mark>
    case 'link': {
      const href = safeHref(mark.attrs?.href)
      return href ? <a key={key} href={href} rel="noreferrer noopener" target="_blank">{child}</a> : <span key={key}>{child}</span>
    }
    case DIFF_ADDED_MARK: return <ins data-change-id={mark.attrs?.change as number | undefined} key={key}>{child}</ins>
    case DIFF_REMOVED_MARK: return <del data-change-id={mark.attrs?.change as number | undefined} key={key}>{child}</del>
    default: return child
  }
}

function renderInline(nodes: PMNode[] | undefined, path: string): ReactNode[] {
  return (nodes ?? []).map((node, index) => {
    const key = `${path}.${index}`
    if (typeof node.text === 'string') return <Fragment key={key}>{renderMarks(node, node.text, `${key}m`)}</Fragment>
    if (node.type === 'hardBreak') return <br key={key}/>
    if (node.type === 'mention') {
      const label = String(node.attrs?.label ?? '')
      const text = node.attrs?.mentionType === 'user' && label && !label.startsWith('@') ? `@${label}` : label
      return <Fragment key={key}>{renderMarks(node, <span className="history-mention">{text}</span>, `${key}m`)}</Fragment>
    }
    if (node.type === 'image') {
      const src = typeof node.attrs?.src === 'string' ? node.attrs.src : ''
      return src ? <Fragment key={key}>{renderMarks(node, <img alt={String(node.attrs?.alt ?? '')} className="history-inline-image" loading="lazy" src={src}/>, `${key}m`)}</Fragment> : null
    }
    return <Fragment key={key}>{renderMarks(node, nodeText(node), `${key}m`)}</Fragment>
  })
}

const CALLOUT_NAMES = new Set(['cyan', 'gray', 'green', 'yellow', 'orange', 'red', 'purple'])

function renderBlocks(nodes: PMNode[] | undefined, path: string): ReactNode[] {
  return (nodes ?? []).map((node, index) => renderBlock(node, `${path}.${index}`))
}

function renderBlock(node: PMNode, key: string): ReactNode {
  const diff = diffAttrs(node)
  const children = node.content
  switch (node.type) {
    case 'paragraph':
      return <p key={key} {...diff}>{children?.length ? renderInline(children, key) : <br className="history-empty-line"/>}</p>
    case 'heading': {
      const level = Math.min(6, Math.max(1, Number(node.attrs?.level) || 1))
      const Tag = `h${level}` as 'h1'
      return <Tag key={key} {...diff}>{renderInline(children, key)}</Tag>
    }
    case 'bulletList': return <ul key={key} {...diff}>{renderBlocks(children, key)}</ul>
    case 'orderedList': return <ol key={key} start={Number(node.attrs?.start) > 1 ? Number(node.attrs?.start) : undefined} {...diff}>{renderBlocks(children, key)}</ol>
    case 'listItem': return <li key={key} {...diff}>{renderBlocks(children, key)}</li>
    case 'taskList': return <ul data-type="taskList" key={key} {...diff}>{renderBlocks(children, key)}</ul>
    case 'taskItem': {
      const checked = Boolean(node.attrs?.checked)
      return <li data-checked={checked ? 'true' : 'false'} key={key} {...diff}><span aria-checked={checked} className="history-checkbox" role="checkbox"/><div>{renderBlocks(children, key)}</div></li>
    }
    case 'blockquote': return <blockquote key={key} {...diff}>{renderBlocks(children, key)}</blockquote>
    case 'horizontalRule': return <hr key={key} {...diff}/>
    case 'codeBlock': {
      const language = typeof node.attrs?.language === 'string' ? node.attrs.language : ''
      return <pre data-language={language || undefined} key={key} {...diff}><code>{renderInline(children, key)}</code></pre>
    }
    case 'table':
      return <div className="history-table-wrap" key={key} {...diff}><table><tbody>{renderBlocks(children, key)}</tbody></table></div>
    case 'tableRow': return <tr key={key}>{renderBlocks(children, key)}</tr>
    case 'tableHeader': return <th key={key}>{renderBlocks(children, key)}</th>
    case 'tableCell': return <td key={key}>{renderBlocks(children, key)}</td>
    case 'callout': {
      const color = String(node.attrs?.color ?? 'cyan')
      const icon = typeof node.attrs?.icon === 'string' && /\p{Extended_Pictographic}/u.test(node.attrs.icon) ? node.attrs.icon : '💡'
      const named = CALLOUT_NAMES.has(color)
      return <aside className={`history-callout${diff.className ? ` ${diff.className}` : ''}`} data-change-id={diff['data-change-id']} data-color={named ? color : 'custom'} key={key} style={!named && /^#[\da-f]{3,8}$/i.test(color) ? ({ '--history-callout-color': color } as CSSProperties) : undefined}><span aria-hidden="true" className="history-callout-icon">{icon}</span><div>{renderBlocks(children, key)}</div></aside>
    }
    case 'details': return <details className="history-details" key={key} open {...diff}>{renderBlocks(children, key)}</details>
    case 'detailsSummary': return <summary key={key}>{renderInline(children, key)}</summary>
    case 'detailsContent': return <div className="history-details-content" key={key}>{renderBlocks(children, key)}</div>
    case 'image': {
      const src = typeof node.attrs?.src === 'string' ? node.attrs.src : ''
      return src ? <p key={key} {...diff}><img alt={String(node.attrs?.alt ?? '')} className="history-image" loading="lazy" src={src}/></p> : null
    }
    case 'embed': {
      const src = String(node.attrs?.src ?? '')
      const embedUrl = String(node.attrs?.embedUrl ?? '')
      const provider = String(node.attrs?.provider ?? '')
      const href = safeHref(src)
      // The player / file card the editor showed, read-only (Open link and Copy link only, as in Linear's history). A version
      // without a usable player URL falls back to the link.
      const playable = href && embedLayoutFor(provider).kind === 'card' ? true : Boolean(href && /^https?:\/\//i.test(embedUrl))
      if (playable) return <div className={`history-embed${diff.className ? ` ${diff.className}` : ''}`} data-change-id={diff['data-change-id']} data-diff={diff['data-diff']} key={key}><ReadOnlyEmbed embedUrl={embedUrl} provider={provider} src={src}/></div>
      return <p key={key} {...diff}>{href ? <a href={href} rel="noreferrer noopener" target="_blank">{src}</a> : src}</p>
    }
    case 'file': case 'video': {
      const href = safeHref(node.attrs?.src ?? node.attrs?.url)
      const name = String(node.attrs?.name ?? node.attrs?.title ?? node.attrs?.src ?? '')
      return <p key={key} {...diff}>{href ? <a href={href} rel="noreferrer noopener" target="_blank">{name}</a> : name}</p>
    }
    case 'diagram': return <pre key={key} {...diff}><code>{String(node.attrs?.source ?? node.attrs?.code ?? nodeText(node))}</code></pre>
    default:
      if (children?.length) {
        const inline = children.every(child => typeof child.text === 'string' || child.type === 'hardBreak' || child.type === 'mention')
        return inline ? <p key={key} {...diff}>{renderInline(children, key)}</p> : <div key={key} {...diff}>{renderBlocks(children, key)}</div>
      }
      return typeof node.text === 'string' ? <p key={key}>{node.text}</p> : null
  }
}

function hasChange(node: PMNode): boolean {
  if (node.attrs?.__diff) return true
  if ((node.marks ?? []).some(mark => mark.type === DIFF_ADDED_MARK || mark.type === DIFF_REMOVED_MARK)) return true
  return (node.content ?? []).some(hasChange)
}

/** Top-level blocks that contain a change get `data-changed` (the preview draws a bar in the margin for them). */
function renderTopLevel(doc: PMNode) {
  return (doc.content ?? []).map((node, index) => {
    const element = renderBlock(node, `b.${index}`)
    return isValidElement(element) && hasChange(node) ? cloneElement(element as ReactElement<Record<string, unknown>>, { 'data-changed': '' }) : element
  })
}

export function DocumentJsonView({ doc, className, emptyLabel, label }: { doc: PMNode; className?: string; emptyLabel?: string; label?: string }) {
  const empty = isEmptyDocument(doc)
  return (
    <div aria-label={label} data-i18n-ignore className={`document-json-view${className ? ` ${className}` : ''}`} role="document">
      {empty ? (emptyLabel ? <p className="history-empty">{emptyLabel}</p> : null) : renderTopLevel(doc)}
    </div>
  )
}
