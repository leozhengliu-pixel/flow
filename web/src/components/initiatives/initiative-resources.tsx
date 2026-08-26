import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ExternalLink, FileText, Link2, MoreHorizontal, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import type { InitiativeResource } from '@/types/flow'

export function InitiativeResources({ initiativeId, resources, onCreate, onUpdate, onDelete }: {
  initiativeId: string
  resources: InitiativeResource[]
  onCreate: (id: string, input: { type?: 'link' | 'document'; title?: string; url: string }) => Promise<InitiativeResource>
  onUpdate: (id: string, resourceId: string, input: { type?: 'link' | 'document'; title?: string; url?: string }) => Promise<InitiativeResource>
  onDelete: (id: string, resourceId: string) => Promise<void>
}) {
  const [dialog, setDialog] = useState<{ resource?: InitiativeResource }>()
  const createDocument = () => onCreate(initiativeId, { type: 'document', title: 'Untitled document', url: `${location.origin}${location.pathname.replace(/\/(overview|activity|projects)$/, '')}/document/${Date.now()}` })
  return <section className="li-resources"><h3>Resources</h3><div className="li-resources__content">
    {resources.map(resource => <div className="li-resource" key={resource.id}>{resource.type === 'document' ? <FileText size={13}/> : <Link2 size={13}/>}<a href={resource.url} rel="noreferrer" target="_blank">{resource.title}</a><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={`${resource.title} actions`} type="button"><MoreHorizontal size={13}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="li-menu" sideOffset={4}><DropdownMenu.Item onSelect={() => window.open(resource.url, '_blank')}><ExternalLink size={14}/>Open</DropdownMenu.Item><DropdownMenu.Item onSelect={() => setDialog({ resource })}><Link2 size={14}/>Edit link</DropdownMenu.Item><DropdownMenu.Separator/><DropdownMenu.Item className="danger" onSelect={() => void onDelete(initiativeId, resource.id)}><Trash2 size={14}/>Delete</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div>)}
    <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="li-resource-add" type="button"><Plus size={14}/>Add document or link…</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="li-menu li-resource-menu" sideOffset={4}><DropdownMenu.Label>Add document or link…</DropdownMenu.Label><DropdownMenu.Item onSelect={() => void createDocument()}><FileText size={14}/>Create new document…</DropdownMenu.Item><DropdownMenu.Item onSelect={() => setDialog({})}><Link2 size={14}/>Add a link…<kbd>Ctrl L</kbd></DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
  </div><InitiativeResourceDialog key={dialog?.resource?.id ?? (dialog ? 'create' : 'closed')} open={Boolean(dialog)} resource={dialog?.resource} onOpenChange={open => { if (!open) setDialog(undefined) }} onSubmit={async input => { if (dialog?.resource) await onUpdate(initiativeId, dialog.resource.id, input); else await onCreate(initiativeId, { type: 'link', title: input.title, url: input.url! }); setDialog(undefined) }}/></section>
}

function InitiativeResourceDialog({ open, resource, onOpenChange, onSubmit }: { open: boolean; resource?: InitiativeResource; onOpenChange: (open: boolean) => void; onSubmit: (input: { title?: string; url?: string }) => Promise<void> }) {
  const [url, setUrl] = useState(resource?.url ?? '')
  const [title, setTitle] = useState(resource?.title ?? '')
  const [saving, setSaving] = useState(false)
  const submit = async () => { if (!url.trim() || saving) return; setSaving(true); try { await onSubmit({ url: url.trim(), title: title.trim() || undefined }) } finally { setSaving(false) } }
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay className="li-dialog-overlay"/><Dialog.Content aria-describedby={undefined} className="li-link-dialog" onOpenAutoFocus={event => { event.preventDefault(); requestAnimationFrame(() => document.querySelector<HTMLInputElement>('.li-link-dialog input')?.focus()) }}>
    <Dialog.Title>{resource ? 'Edit initiative link' : 'Add link to initiative'}</Dialog.Title>
    <label><span>URL</span><input aria-label="URL" placeholder="https://…" value={url} onChange={event => setUrl(event.target.value)} onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') void submit() }}/></label>
    <label><span>Title<span className="li-optional">(optional)</span></span><input aria-label="Title(optional)" value={title} onChange={event => setTitle(event.target.value)}/></label>
    <footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button disabled={!url.trim() || saving} onClick={() => void submit()} type="button">{saving ? 'Adding…' : resource ? 'Save' : 'Add link'}</button></footer>
  </Dialog.Content></Dialog.Portal></Dialog.Root>
}
