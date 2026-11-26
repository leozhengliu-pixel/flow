import { useRef, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as Popover from '@radix-ui/react-popover'
import { ExternalLink, File, FileText, Link2, Pencil, Plus, Trash2 } from 'lucide-react'
import { formatFileSize } from '@/lib/format-file-size'
import { DocumentGlyph } from '@/components/documents/document-icon'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { useCurrentTimeMs } from '@/hooks/use-current-time'
import { useI18n } from '@/i18n/i18n'
import { documentPath } from '@/lib/app-routes'
import { formatRelativeTime } from '@/lib/relative-time'
import type { Attachment, FlowDocument } from '@/types/flow'
import type { IssueOptionsActions } from '@/components/issue/issue-options-menu'
import { LinkBrandIcon } from './link-brand-icon'
import { FlowUrlIcon } from '@/components/issue/flow-header-icons'
import { defaultLinkTitle, linkBrand } from '@/lib/link-brand'
import './issue-resources.css'

const safeHost = (value: string) => { try { return new URL(value).hostname } catch { return 'Link' } }
const caret = <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path d="M10.6239 6.75194C10.8183 6.41861 10.5779 6 10.192 6H5.80802C5.42212 6 5.18169 6.41861 5.37613 6.75194L7.56811 10.5096C7.76105 10.8404 8.23895 10.8404 8.43189 10.5096L10.6239 6.75194Z"/></svg>

/** A site's favicon; an icon that cannot be loaded gives way to the generic link glyph. */
function LinkFavicon({ src }: { src: string }) {
  const [failed, setFailed] = useState(false)
  if (failed) return <FlowUrlIcon className="res-link-glyph"/>
  return <img alt="" height={16} loading="lazy" onError={() => setFailed(true)} referrerPolicy="no-referrer" src={src} width={16}/>
}

/** The issue's Resources section (the design reference's layout): a collapsible header with an add button, then one row per linked document or link. */
export function IssueResources({ documents, links, files = [], workspaceKey, issueId, issueKey, actions, onDeleteLink, onEditLink, readOnly = false }: { documents: FlowDocument[]; links: Attachment[]; files?: Attachment[]; workspaceKey: string; issueId: string; issueKey: string; actions?: IssueOptionsActions; onDeleteLink: (id: string) => Promise<void>; onEditLink?: (id: string, input: { url: string; title: string }) => Promise<void>; /** The viewer may only read: no add button, no Edit… or Remove. */ readOnly?: boolean }) {
  const { t, locale } = useI18n()
  const nowMs = useCurrentTimeMs()
  const [collapsed, setCollapsed] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const [linkOpen, setLinkOpen] = useState(false)
  const [url, setUrl] = useState('')
  const [title, setTitle] = useState('')
  const [saving, setSaving] = useState(false)
  const [editing, setEditing] = useState<Attachment | null>(null)
  const urlInput = useRef<HTMLInputElement>(null)
  const titleInput = useRef<HTMLInputElement>(null)
  const openEdit = (link: Attachment) => { setEditing(link); setUrl(link.url); setTitle(link.title); setLinkOpen(true) }
  const closeDialog = (open: boolean) => { setLinkOpen(open); if (!open) { setEditing(null); setUrl(''); setTitle('') } }
  const addLink = async () => {
    if (!url.trim()) return
    setSaving(true)
    try {
      if (editing) await onEditLink?.(editing.id, { url: url.trim(), title: title.trim() })
      else if (actions) await actions.addLink({ url: url.trim(), title: title.trim() || undefined })
      else return
      closeDialog(false)
    } finally { setSaving(false) }
  }
  return <section className="res-section">
    <header className="res-header">
      <button aria-expanded={!collapsed} aria-label={t(collapsed ? 'Expand resources section' : 'Collapse resources section')} className="res-collapse" onClick={() => setCollapsed(value => !value)} type="button">
        <span className="res-caret" data-collapsed={collapsed || undefined}>{caret}</span><span>{t('Resources')}</span>
      </button>
      {!readOnly && <Popover.Root open={menuOpen} onOpenChange={setMenuOpen}>
        <Popover.Trigger asChild><button aria-label={t('Add resource to issue')} className="res-add" type="button"><Plus size={14}/></button></Popover.Trigger>
        <Popover.Portal><Popover.Content align="end" className="issue-resource-menu" data-flow-motion="floating" sideOffset={4}>
          <button disabled={!actions} onClick={() => { setMenuOpen(false); closeDialog(true) }} type="button"><Link2/><span>{t('Add link…')}</span><kbd>Ctrl L</kbd></button>
          <button disabled={!actions} onClick={() => { setMenuOpen(false); void actions?.addDocument() }} type="button"><FileText/><span>{t('Add document')}</span></button>
        </Popover.Content></Popover.Portal>
      </Popover.Root>}
    </header>
    {!collapsed && <div className="res-rows">
      {documents.map(document => <a className="res-row" href={documentPath(workspaceKey, document)} key={document.id}><span className="res-icon"><DocumentGlyph document={document}/></span><span className="res-title" data-i18n-ignore>{document.title}</span></a>)}
      {[...links, ...files].map(link => {
        const isFile = link.contentType !== 'text/uri-list'
        const when = link.createdAt ? formatRelativeTime(link.createdAt, locale, nowMs) : ''
        const brand = isFile ? null : linkBrand(link.url)
        let faviconURL = link.iconUrl
        if (!isFile && !brand && !faviconURL) {
          const query = new URLSearchParams({ workspace: workspaceKey, issueId, attachmentId: link.id })
          if (link.updatedAt) query.set('v', link.updatedAt)
          faviconURL = `/api/link-icon?${query}`
        }
        return <a className="res-row" href={link.url} key={link.id} rel="noreferrer" target="_blank">
          <span className="res-icon" data-brand={brand ?? undefined}>{isFile && link.contentType.startsWith('image/') ? <img alt="" height={16} src={link.url} width={16}/> : isFile ? <File size={16}/> : brand ? <LinkBrandIcon brand={brand}/> : faviconURL && <LinkFavicon key={faviconURL} src={faviconURL}/>}</span>
          <span className="res-title" data-i18n-ignore>{(!link.title || link.title === safeHost(link.url)) && !isFile ? defaultLinkTitle(link.url) : link.title || safeHost(link.url)}</span>
          {(link.subtitle || isFile) && <span className="res-subtitle" data-i18n-ignore>{link.subtitle || formatFileSize(link.size)}</span>}
          <span className="res-meta"><span className="res-time">{when}</span>
            <span className="res-menu-slot" onClick={event => { event.preventDefault(); event.stopPropagation() }}>
              <DropdownMenu>
                <DropdownMenuTrigger asChild><button aria-label={t('Open menu')} className="res-menu" type="button"><svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path d="M3 6.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Z"/></svg></button></DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onSelect={() => window.open(link.url, '_blank', 'noopener,noreferrer')}><ExternalLink size={16}/>{t('Open link')}</DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => void navigator.clipboard.writeText(link.url)}><Link2 size={16}/>{t('Copy link')}</DropdownMenuItem>
                  {!readOnly && !isFile && onEditLink && <><DropdownMenuSeparator/><DropdownMenuItem onSelect={() => openEdit(link)}><Pencil size={16}/>{t('Edit…')}</DropdownMenuItem></>}
                  {!readOnly && <DropdownMenuSeparator/>}
                  {!readOnly && <DropdownMenuItem onSelect={() => void onDeleteLink(link.id)}><Trash2 size={16}/>{t('Remove')}</DropdownMenuItem>}
                </DropdownMenuContent>
              </DropdownMenu>
            </span>
          </span>
        </a>
      })}
    </div>}
    <Dialog.Root open={linkOpen} onOpenChange={closeDialog}>
      <Dialog.Portal>
        <Dialog.Overlay className="res-dialog-overlay" data-flow-motion="backdrop"/>
        <Dialog.Content className="res-dialog" data-flow-motion="dialog" onOpenAutoFocus={event => { event.preventDefault(); const input = (editing ? titleInput : urlInput).current; input?.focus(); input?.setSelectionRange(input.value.length, input.value.length); }}>
          <form onSubmit={event => { event.preventDefault(); void addLink() }}>
            <Dialog.Title className="res-dialog-title">{editing && linkBrand(editing.url) ? <LinkBrandIcon brand={linkBrand(editing.url)!}/> : <FlowUrlIcon/>}<span>{editing ? t('Edit link') : t('Add link to {issue}').replace('{issue}', () => issueKey)}</span></Dialog.Title>
            <div className="res-field"><label htmlFor="res-link-url"><span>{t('URL')}</span></label><input disabled={Boolean(editing)} id="res-link-url" ref={urlInput} onChange={event => setUrl(event.target.value)} placeholder="https://…" type="url" value={url}/></div>
            <div className="res-field"><label htmlFor="res-link-title"><span>{t('Title')}</span><em>{t('(optional)')}</em></label><input id="res-link-title" ref={titleInput} onFocus={event => { const input = event.currentTarget; if (editing) queueMicrotask(() => input.setSelectionRange(input.value.length, input.value.length)) }} onChange={event => setTitle(event.target.value)} value={title}/></div>
            <footer><Dialog.Close asChild><button type="button">{t('Cancel')}</button></Dialog.Close><button className="primary" disabled={!url.trim() || saving} type="submit">{editing ? t(saving ? 'Saving…' : 'Save') : t(saving ? 'Adding…' : 'Add link')}</button></footer>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  </section>
}
