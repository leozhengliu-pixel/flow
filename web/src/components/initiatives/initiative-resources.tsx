import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ExternalLink, FileText, Link2, MoreHorizontal, Plus, Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import type { BootstrapData, FlowDocument, InitiativeResource } from '@/types/flow'
import { createDocument } from '@/lib/api'
import { documentPath } from '@/lib/app-routes'
import { createDocumentIn, type DocumentActionContext } from '@/components/documents/document-actions'
import { DocumentMenuItems } from '@/components/documents/document-menu'
import { DocumentGlyph } from '@/components/documents/document-icon'
import { resourceChipTitle } from '@/components/project-detail/project-resource-link-name'
import { AppLink } from '@/components/ui/app-link'
import { LinearDropdownMenuContent, LinearMenuItem, LinearMenuSeparator } from '@/components/ui/row-context-menu'
import { useOptionalRouteNavigation } from '@/hooks/use-optional-route-navigation'
import { useI18n } from '@/i18n/i18n'

export function InitiativeResources({ data, documents, initiativeId, resources, onCreate, onUpdate, onDelete, onReload }: {
  data?: BootstrapData
  documents: FlowDocument[]
  initiativeId: string
  resources: InitiativeResource[]
  onCreate: (id: string, input: { type?: 'link' | 'document'; title?: string; url?: string; documentId?: string }) => Promise<InitiativeResource>
  onUpdate: (id: string, resourceId: string, input: { type?: 'link' | 'document'; title?: string; url?: string; documentId?: string }) => Promise<InitiativeResource>
  onDelete: (id: string, resourceId: string) => Promise<void>
  onReload?: () => Promise<void>
}) {
  const { t } = useI18n()
  const navigate = useOptionalRouteNavigation()
  const [dialog, setDialog] = useState<{ resource?: InitiativeResource }>()
  const ctx = useMemo<DocumentActionContext | undefined>(() => data ? { data, reload: () => onReload?.() ?? Promise.resolve(), navigate, t } : undefined, [data, navigate, onReload, t])
  // "New document" creates an untitled document linked to this initiative and opens it.
  const createBoundDocument = async () => {
    if (!ctx) {
      const document = await createDocument({ title: '' })
      await onCreate(initiativeId, { type: 'document', documentId: document.id })
      return
    }
    const created = await createDocumentIn(ctx, { type: 'initiative', id: initiativeId })
    if (created) ctx.navigate(documentPath(ctx.data.workspace.urlKey, created))
  }
  return <section className="li-resources"><h3>Resources</h3><div className="li-resources__content">
    {resources.map(resource => {
      const document = resource.type === 'document' ? documents.find(item => item.id === resource.documentId) : undefined
      const title = resourceChipTitle(resource, document, t)
      const glyph = document ? <DocumentGlyph document={document}/> : resource.type === 'document' ? <FileText size={13}/> : <Link2 size={13}/>
      return <div className="li-resource" key={resource.id}>
        {glyph}
        {resource.type === 'document'
          ? <AppLink data-i18n-ignore href={resource.url}>{title}</AppLink>
          : <a data-i18n-ignore href={resource.url} rel="noreferrer" target="_blank">{title}</a>}
        <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={`${title} actions`} data-i18n-ignore type="button"><MoreHorizontal size={13}/></button></DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            {document && ctx
              ? <LinearDropdownMenuContent align="end" className="li-resource-action-menu" label={t('Document actions')}>
                <DocumentMenuItems ctx={ctx} document={document} variant="resource"/>
                <LinearMenuSeparator/>
                <LinearMenuItem label="Remove resource" onSelect={() => void onDelete(initiativeId, resource.id)}/>
              </LinearDropdownMenuContent>
              : <DropdownMenu.Content data-flow-motion="floating" align="end" className="li-menu" sideOffset={4}>
                <DropdownMenu.Item onSelect={() => { if (resource.type === 'document') navigate(resource.url); else window.open(resource.url, '_blank') }}><ExternalLink size={14}/>{t('Open')}</DropdownMenu.Item>
                {resource.type === 'link' && <DropdownMenu.Item onSelect={() => setDialog({ resource })}><Link2 size={14}/>{t('Edit link')}</DropdownMenu.Item>}
                <DropdownMenu.Separator/>
                <DropdownMenu.Item className="danger" onSelect={() => void onDelete(initiativeId, resource.id)}><Trash2 size={14}/>{t('Delete')}</DropdownMenu.Item>
              </DropdownMenu.Content>}
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      </div>
    })}
    <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="li-resource-add" type="button"><Plus size={14}/>Add document or link…</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="start" className="li-menu li-resource-menu" sideOffset={4}><DropdownMenu.Label>Add document or link…</DropdownMenu.Label><DropdownMenu.Item onSelect={() => void createBoundDocument()}><FileText size={14}/>Create new document…</DropdownMenu.Item><DropdownMenu.Item onSelect={() => setDialog({})}><Link2 size={14}/>Add a link…<kbd>Ctrl L</kbd></DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
  </div><InitiativeResourceDialog key={dialog?.resource?.id ?? (dialog ? 'create' : 'closed')} open={Boolean(dialog)} resource={dialog?.resource} onOpenChange={open => { if (!open) setDialog(undefined) }} onSubmit={async input => { if (dialog?.resource) await onUpdate(initiativeId, dialog.resource.id, input); else await onCreate(initiativeId, { type: 'link', title: input.title, url: input.url! }); setDialog(undefined) }}/></section>
}

function InitiativeResourceDialog({ open, resource, onOpenChange, onSubmit }: { open: boolean; resource?: InitiativeResource; onOpenChange: (open: boolean) => void; onSubmit: (input: { title?: string; url?: string }) => Promise<void> }) {
  const [url, setUrl] = useState(resource?.url ?? '')
  const [title, setTitle] = useState(resource?.title ?? '')
  const [saving, setSaving] = useState(false)
  const submit = async () => { if (!url.trim() || saving) return; setSaving(true); try { await onSubmit({ url: url.trim(), title: title.trim() || undefined }) } finally { setSaving(false) } }
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="li-dialog-overlay"/><Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} className="li-link-dialog" onOpenAutoFocus={event => { event.preventDefault(); requestAnimationFrame(() => document.querySelector<HTMLInputElement>('.li-link-dialog input')?.focus()) }}>
    <Dialog.Title>{resource ? 'Edit initiative link' : 'Add link to initiative'}</Dialog.Title>
    <label><span>URL</span><input aria-label="URL" placeholder="https://…" value={url} onChange={event => setUrl(event.target.value)} onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') void submit() }}/></label>
    <label><span>Title<span className="li-optional">(optional)</span></span><input aria-label="Title(optional)" value={title} onChange={event => setTitle(event.target.value)}/></label>
    <footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button disabled={!url.trim() || saving} onClick={() => void submit()} type="button">{saving ? 'Adding…' : resource ? 'Save' : 'Add link'}</button></footer>
  </Dialog.Content></Dialog.Portal></Dialog.Root>
}
