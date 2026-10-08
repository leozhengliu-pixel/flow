import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { ChevronDown, Paperclip, X, Check } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { toast } from 'sonner'

import { StatusIcon } from '@/components/issue/issue-icons'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { LinearDropdownMenuContent, LinearMenuItem, LinearMenuSearch } from '@/components/ui/row-context-menu'
import { FlowTooltip } from '@/components/ui/tooltip'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { createCustomerRequest, createIssue, listIssueRecords, updateCustomerRequest, uploadCustomerRequestAttachment } from '@/lib/api'
import type { BootstrapData, Customer, CustomerRequest, Issue, Project } from '@/types/flow'
import { ComposerBranchIcon, EditPencilIcon, IssueCircleIcon, LinkIcon } from './customer-page-icons'
import { customerRequestIssueState, customerRequestIssueTeam, newIssueTitleFor } from './customer-page-model'

export type ComposerTarget = { kind: 'new' } | { kind: 'issue'; issue: Issue } | { kind: 'project'; project: Project }

type ComposerProps = {
  data: BootstrapData
  customer: Customer
  /** `customer`: the customer page's own composer (picks or creates the issue); `feature`: a new request on a row's issue / project; `edit`: an existing request. */
  mode: 'customer' | 'feature' | 'edit'
  request?: CustomerRequest
  issue?: Issue
  project?: Project
  onClose: () => void
  /** Called after a save with the issues whose requests changed. */
  onSaved: (issueIds: string[]) => Promise<void> | void
  className?: string
}

function normalizeUrl(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return ''
  const withScheme = trimmed.includes('://') ? trimmed : `https://${trimmed}`
  try { return new URL(withScheme).toString() } catch { return undefined }
}

/**
 * Linear's request composer (CustomerPage.kn / AdditionalCustomerNeedCreateForm.J): the request text,
 * a Source link, attachments, Cancel and the submit button, and — on the customer page — the
 * "This request will be added to" bar that picks an issue or project or names a new issue.
 */
export function CustomerNeedComposer({ data, customer, mode, request, issue, project, onClose, onSaved, className = '' }: ComposerProps) {
  const { t } = useI18n()
  const [body, setBody] = useState(request?.body ?? '')
  const [source, setSource] = useState(request?.sourceUrl ?? '')
  const [files, setFiles] = useState<File[]>([])
  const [target, setTarget] = useState<ComposerTarget>({ kind: 'new' })
  const initialTitle = newIssueTitleFor(customer)
  const [newTitle, setNewTitle] = useState(initialTitle)
  const [saving, setSaving] = useState(false)
  const bodyRef = useRef<HTMLTextAreaElement>(null)
  const fileRef = useRef<HTMLInputElement>(null)
  const formRef = useRef<HTMLFormElement>(null)

  useEffect(() => {
    const field = bodyRef.current
    if (!field) return
    field.focus()
    field.setSelectionRange(field.value.length, field.value.length)
  }, [])
  useEffect(() => {
    const field = bodyRef.current
    if (!field) return
    field.style.height = 'auto'
    field.style.height = `${Math.min(mode === 'customer' ? 200 : 300, Math.max(mode === 'customer' ? 60 : 72, field.scrollHeight))}px`
  }, [body, mode])

  const hasChanges = body.trim() !== (request?.body ?? '').trim() || source.trim() !== (request?.sourceUrl ?? '') || files.length > 0 || target.kind !== 'new' || newTitle !== initialTitle

  const discard = async () => {
    if (!hasChanges) { onClose(); return }
    const confirmed = await confirmAction(t('Discard this request?'), { description: t('Confirm that you want to discard this customer request.'), confirmLabel: t('Discard'), danger: true })
    if (confirmed) onClose()
  }

  const submit = async (event?: FormEvent) => {
    event?.preventDefault()
    if (saving) return
    const sourceUrl = normalizeUrl(source)
    if (sourceUrl === undefined) { toast.error(t('Invalid URL, please enter a valid URL')); return }
    const text = body.trim()
    if (mode === 'customer' && target.kind === 'new' && !text && !sourceUrl) {
      toast.warning(t('Request details or source required'), { description: t('When linked to a new issue, the request must have details or a source.') })
      return
    }
    setSaving(true)
    try {
      if (mode === 'edit' && request) {
        await updateCustomerRequest(request.id, { body: text, sourceUrl: sourceUrl ?? '' })
        toast.success(t('Customer request updated'))
        onClose()
        await onSaved(request.issueId ? [request.issueId] : [])
        return
      }
      let issueId = issue?.id
      let projectId = issue ? undefined : project?.id
      let targetName = issue ? `${issue.identifier} ${issue.title}` : project?.name
      if (mode === 'customer') {
        if (target.kind === 'new') {
          const team = customerRequestIssueTeam(data)
          if (!team) throw new Error(t('Create a team before adding requests'))
          const created = await createIssue({ title: newTitle.trim() || initialTitle, description: '', teamId: team.id, stateId: customerRequestIssueState(data, team.id)?.id, priority: 0 })
          issueId = created.id
          targetName = `${created.identifier} ${created.title}`
        } else if (target.kind === 'issue') {
          issueId = target.issue.id
          targetName = `${target.issue.identifier} ${target.issue.title}`
        } else {
          projectId = target.project.id
          targetName = target.project.name
        }
      }
      const created = await createCustomerRequest({ customerId: customer.id, body: text, source: 'manual', sourceUrl: sourceUrl || undefined, issueId, projectId })
      await Promise.all(files.map(file => uploadCustomerRequestAttachment(created.id, file)))
      if (mode === 'customer') toast.success(t('Request from {customer} added to {target}').replace('{customer}', customer.name).replace('{target}', targetName ?? ''))
      else toast.success(t('Customer request added'))
      onClose()
      await onSaved(issueId ? [issueId] : [])
    } catch (error) {
      toast.error(t('Failed to save customer request'), { description: error instanceof Error ? error.message : undefined })
    } finally {
      setSaving(false)
    }
  }

  const onKeyDown = (event: KeyboardEvent<HTMLFormElement>) => {
    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) { event.preventDefault(); void submit(); return }
    if (event.key === 'Escape' && !event.defaultPrevented) {
      const target = event.target as HTMLElement
      if (target.closest('[role=menu],[role=dialog]')) return
      event.preventDefault()
      event.stopPropagation()
      void discard()
    }
  }

  const submitLabel = mode === 'customer' ? 'Add request' : mode === 'edit' ? 'Save' : 'Create'
  const submitHint = mode === 'customer' ? 'to add request' : 'to create request'
  const editor = <div className="customer-need-composer__surface">
    <textarea ref={bodyRef} className="customer-need-composer__input" aria-label={t(mode === 'customer' ? 'Note' : 'Request')} placeholder={t('Add request details')} value={body} onChange={event => setBody(event.target.value)} rows={3}/>
    {files.length > 0 && <div className="customer-need-composer__files">
      {files.map((file, index) => <span key={`${file.name}:${index}`}>
        <Paperclip size={12} aria-hidden="true"/><span data-i18n-ignore>{file.name}</span>
        <button type="button" aria-label={t('Remove {name}').replace('{name}', file.name)} onClick={() => setFiles(current => current.filter((_, itemIndex) => itemIndex !== index))}><X size={11}/></button>
      </span>)}
    </div>}
    <div className="customer-need-composer__toolbar">
      <SourceButton value={source} onChange={setSource}/>
      <div className="customer-need-composer__actions">
        <FlowTooltip label={t('Attach images, files or videos')}>
          <button type="button" className="customer-need-composer__icon" aria-label={t('Attach images, files or videos')} onClick={() => fileRef.current?.click()}><Paperclip size={14}/></button>
        </FlowTooltip>
        <input ref={fileRef} hidden multiple type="file" onChange={event => { setFiles(current => [...current, ...Array.from(event.target.files ?? [])]); event.target.value = '' }}/>
        <FlowTooltip label={t('Discard')} shortcut="Esc">
          <button type="button" className="customer-need-composer__cancel" onClick={() => void discard()}>{t('Cancel')}</button>
        </FlowTooltip>
        <FlowTooltip label={<>{t('Press')} <kbd>⌘</kbd><kbd>↵</kbd> {t(submitHint)}</>}>
          <button type="submit" className="customer-need-composer__submit" disabled={saving}>{t(submitLabel)}</button>
        </FlowTooltip>
      </div>
    </div>
  </div>

  return <form ref={formRef} className={`customer-need-composer is-${mode} ${className}`} onSubmit={event => void submit(event)} onKeyDown={onKeyDown} aria-label={t(mode === 'edit' ? 'Edit request' : 'Add customer request')}>
    {editor}
    {mode === 'customer' && <TargetBar data={data} target={target} onTarget={setTarget} newTitle={newTitle} onNewTitle={setNewTitle}/>}
  </form>
}

function SourceButton({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState(value)
  useEffect(() => { if (open) setDraft(value) }, [open, value])
  const host = (() => { const url = normalizeUrl(value); try { return url ? new URL(url).hostname : '' } catch { return '' } })()
  const commit = () => {
    const url = normalizeUrl(draft)
    if (url === undefined) { toast.error(t('Invalid URL, please enter a valid URL')); return }
    onChange(url)
    setOpen(false)
  }
  return <Popover.Root open={open} onOpenChange={next => { if (!next && open) commit(); else setOpen(next) }}>
    <FlowTooltip label={host ? normalizeUrl(value) : t('Add source')} disabled={open}>
      <Popover.Trigger asChild>
        <button type="button" className="customer-need-composer__source" aria-haspopup="dialog"><LinkIcon size={14}/><span data-i18n-ignore={host ? '' : undefined}>{host ? t('via {host}').replace('{host}', host) : t('Source')}</span></button>
      </Popover.Trigger>
    </FlowTooltip>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating" className="customer-need-composer__source-popover" align="start" side="bottom" sideOffset={4} collisionPadding={8} onKeyDown={event => {
        if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); setOpen(false) }
      }}>
        <input autoFocus aria-label={t('Paste link…')} placeholder={t('Paste link…')} value={draft} onChange={event => setDraft(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); event.stopPropagation(); commit() } }}/>
        <span aria-hidden="true" className="customer-need-composer__source-divider"/>
        <FlowTooltip label={t('Remove link')}>
          <button type="button" aria-label={t('Clear source')} onClick={() => { setDraft(''); onChange(''); setOpen(false) }}><X size={12}/></button>
        </FlowTooltip>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function TargetBar({ data, target, onTarget, newTitle, onNewTitle }: { data: BootstrapData; target: ComposerTarget; onTarget: (target: ComposerTarget) => void; newTitle: string; onNewTitle: (title: string) => void }) {
  const { t } = useI18n()
  const [editingTitle, setEditingTitle] = useState(false)
  const [titleDraft, setTitleDraft] = useState(newTitle)
  const titleRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (!editingTitle) return
    setTitleDraft(newTitle)
    requestAnimationFrame(() => { titleRef.current?.focus(); titleRef.current?.select(); if (titleRef.current) titleRef.current.scrollLeft = 0 })
  }, [editingTitle]) // eslint-disable-line react-hooks/exhaustive-deps
  const saveTitle = () => { if (titleDraft.trim()) onNewTitle(titleDraft.trim()); setEditingTitle(false) }
  const backlog = customerRequestIssueState(data, customerRequestIssueTeam(data)?.id)
  return <div className="customer-need-composer__target">
    <span className="customer-need-composer__target-label"><ComposerBranchIcon/>{t('This request will be added to')}</span>
    {editingTitle && target.kind === 'new'
      ? <span className="customer-need-composer__title-edit">
        {backlog && <StatusIcon state={backlog} size={14}/>}
        <span className="customer-need-composer__muted">{t('New issue')}</span>
        <input ref={titleRef} aria-label={t('Edit new issue title')} value={titleDraft} onChange={event => setTitleDraft(event.target.value)} onKeyDown={event => {
          if (event.key === 'Enter') { event.preventDefault(); event.stopPropagation(); saveTitle() }
          if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); setEditingTitle(false) }
        }}/>
        <FlowTooltip label={t('Cancel')}><button type="button" className="customer-need-composer__icon" aria-label={t('Cancel')} onClick={() => setEditingTitle(false)}><X size={14}/></button></FlowTooltip>
        <FlowTooltip label={t('Save new title')}><button type="button" className="customer-need-composer__icon" aria-label={t('Save')} onClick={saveTitle}><Check size={14}/></button></FlowTooltip>
      </span>
      : <>
        <TargetPicker data={data} target={target} onTarget={onTarget} newTitle={newTitle}/>
        {target.kind === 'new' && <FlowTooltip label={t('Edit new issue title')}>
          <button type="button" className="customer-need-composer__icon customer-need-composer__edit-title" aria-label={t('Edit new issue title')} onClick={() => setEditingTitle(true)}><EditPencilIcon size={14}/></button>
        </FlowTooltip>}
      </>}
  </div>
}

function TargetPicker({ data, target, onTarget, newTitle }: { data: BootstrapData; target: ComposerTarget; onTarget: (target: ComposerTarget) => void; newTitle: string }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<Issue[]>([])
  const backlog = customerRequestIssueState(data, customerRequestIssueTeam(data)?.id)
  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      listIssueRecords({ q: query.trim() || undefined, archived: 'false', limit: query.trim() ? 10 : 5, sort: 'updatedAt', direction: 'desc' }, controller.signal)
        .then(page => setResults(page.items))
        .catch(() => undefined)
    }, query ? 150 : 0)
    return () => { window.clearTimeout(timer); controller.abort() }
  }, [open, query])
  useEffect(() => { if (!open) setQuery('') }, [open])
  const projects = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase()
    const live = data.projects.filter(item => !item.archivedAt)
    return normalized ? live.filter(item => item.name.toLocaleLowerCase().includes(normalized)).slice(0, 10) : [...live].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)).slice(0, 5)
  }, [data.projects, query])
  const label = target.kind === 'issue'
    ? <><span className="customer-need-composer__muted">{target.issue.identifier}</span><span className="customer-need-composer__strong" data-i18n-ignore>{target.issue.title}</span></>
    : target.kind === 'project'
      ? <span className="customer-need-composer__base" data-i18n-ignore>{target.project.name}</span>
      : <><span className="customer-need-composer__muted">{t('New issue')}</span>{newTitle !== 'New issue' && <span className="customer-need-composer__strong" data-i18n-ignore>{newTitle}</span>}</>
  const icon = target.kind === 'issue' ? <StatusIcon state={target.issue.state} size={14}/> : target.kind === 'project' ? <ViewGlyph color={target.project.color} icon={target.project.icon}/> : backlog ? <StatusIcon state={backlog} size={14}/> : <IssueCircleIcon size={14}/>
  return <DropdownMenu.Root open={open} onOpenChange={setOpen}>
    <DropdownMenu.Trigger asChild>
      <button type="button" className="customer-need-composer__picker" aria-label={t('Search issues or projects…')}>{icon}{label}<ChevronDown size={12} aria-hidden="true"/></button>
    </DropdownMenu.Trigger>
    <LinearDropdownMenuContent label={t('Search issues or projects…')} className="has-search customer-need-composer__picker-menu">
      <LinearMenuSearch value={query} onChange={setQuery} placeholder="Search issues or projects…"/>
      <div className="linear-menu__list">
        <LinearMenuItem icon={backlog ? <StatusIcon state={backlog} size={14}/> : <IssueCircleIcon size={14}/>} label="New issue" detail={newTitle !== 'New issue' ? <span data-i18n-ignore>{newTitle}</span> : undefined} onSelect={() => onTarget({ kind: 'new' })}/>
        {target.kind === 'issue' && !results.some(item => item.id === target.issue.id) && <LinearMenuItem icon={<StatusIcon state={target.issue.state} size={14}/>} label={`${target.issue.identifier} ${target.issue.title}`} translate={false} onSelect={() => onTarget({ kind: 'new' })}/>}
        {projects.length > 0 && <div className="linear-menu__group-label">{t(query ? 'Projects' : 'Recent projects')}</div>}
        {projects.map(item => <LinearMenuItem key={item.id} icon={<ViewGlyph color={item.color} icon={item.icon}/>} label={item.name} translate={false} onSelect={() => onTarget({ kind: 'project', project: item })}/>)}
        {results.length > 0 && <div className="linear-menu__group-label">{t(query ? 'Issues' : 'Recent issues')}</div>}
        {results.map(item => <LinearMenuItem key={item.id} icon={<StatusIcon state={item.state} size={14}/>} label={`${item.identifier} ${item.title}`} translate={false} onSelect={() => onTarget({ kind: 'issue', issue: item })}/>)}
        {query && !projects.length && !results.length && <div className="linear-menu__empty">{t('No matching issues or projects')}</div>}
      </div>
    </LinearDropdownMenuContent>
  </DropdownMenu.Root>
}
