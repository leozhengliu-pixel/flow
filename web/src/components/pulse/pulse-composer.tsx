import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Check, Paperclip, X } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import type { Initiative, InitiativeUpdate, Project, ProjectUpdate, User } from '@/types/flow'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import type { DescriptionSnapshot } from '@/components/issue/editor/editor-content'
import { useI18n } from '@/i18n/i18n'
import { PulseComposerDiffPreview } from './pulse-composer-diff'
import { HEALTH_LABELS } from './pulse-card-model'

type Source = { kind: 'project'; entity: Project } | { kind: 'initiative'; entity: Initiative }

export function PulseComposer({ initiatives, onCreateInitiative, onCreateProject, onUploadInitiativeAttachment, onUploadProjectAttachment, onOpenChange, open, projects, users }: {
  initiatives: Initiative[]
  onCreateInitiative: (id: string, input: { body: string;bodyData?:Record<string,unknown>;health?: Project['health'] }) => Promise<InitiativeUpdate>
  onCreateProject: (id: string, input: { body: string;bodyData?:Record<string,unknown>;health?: Project['health'] }) => Promise<ProjectUpdate>
  onUploadInitiativeAttachment:(id:string,updateId:string,file:File)=>Promise<InitiativeUpdate>
  onUploadProjectAttachment:(id:string,updateId:string,file:File)=>Promise<ProjectUpdate>
  onOpenChange: (open: boolean) => void
  open: boolean
  projects: Project[]
  users: User[]
}) {
  const sources: Source[] = useMemo(() => [...projects.map(entity => ({ kind: 'project' as const, entity })), ...initiatives.map(entity => ({ kind: 'initiative' as const, entity }))], [initiatives, projects])
  const [source, setSource] = useState<Source | undefined>(sources[0])
  const [health, setHealth] = useState<Project['health']>('onTrack')
  const [body, setBody] = useState<DescriptionSnapshot>()
  const [files,setFiles]=useState<File[]>([])
  const [saving, setSaving] = useState(false)
  const fileRef=useRef<HTMLInputElement>(null)
  useEffect(() => { if (open && !source) setSource(sources[0]) }, [open, source, sources])
  useEffect(() => { if (!open) { setBody(undefined);setFiles([]);setSaving(false) } }, [open])
  const submit = async () => {
    if (!source || !body?.markdown.trim() || saving) return
    setSaving(true)
    try {
      const input={body:body.markdown.trim(),bodyData:body.document as Record<string,unknown>,health}
      if (source.kind === 'project') { const update=await onCreateProject(source.entity.id,input);for(const file of files)await onUploadProjectAttachment(source.entity.id,update.id,file) }
      else { const update=await onCreateInitiative(source.entity.id,input);for(const file of files)await onUploadInitiativeAttachment(source.entity.id,update.id,file) }
      onOpenChange(false)
    } finally { setSaving(false) }
  }
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="pulse-dialog-overlay"/><Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} aria-label={`Create ${source?.kind ?? ''} update`} className="pulse-composer">
    <header>
      <SourceMenu initiatives={initiatives} projects={projects} source={source} onChange={setSource}/>
      <PulseHealthMenu health={health} kind={source?.kind ?? 'project'} onChange={setHealth}/>
      <Dialog.Close asChild><button aria-label="Close dialog" className="pulse-composer-close" type="button"><X size={15}/></button></Dialog.Close>
    </header>
    <IssueDescriptionEditor users={users} ariaLabel={source?.kind==='initiative'?'Initiative update':'Project update'} className="pulse-composer-editor" placeholder={source?.kind==='initiative'?'Write an initiative update…':'Write a project update…'} value={body?.markdown??''} state={body?.documentJSON} onChange={setBody} onSubmit={()=>void submit()}/>
    {source && <PulseComposerDiffPreview source={source}/>}
    {files.length>0&&<div className="pulse-composer-files">{files.map((file,index)=><span key={`${file.name}:${index}`}>{file.name}<button aria-label={`Remove ${file.name}`} onClick={()=>setFiles(current=>current.filter((_,itemIndex)=>itemIndex!==index))}><X size={11}/></button></span>)}</div>}
    <footer><button aria-label="Attach images, files, or videos" onClick={()=>fileRef.current?.click()} type="button"><Paperclip size={15}/></button><input ref={fileRef} type="file" hidden multiple onChange={event=>{setFiles(Array.from(event.target.files??[]));event.target.value=''}}/><span/><button className="pulse-post-button" disabled={!body?.markdown.trim() || !source || saving} onClick={() => void submit()} type="button">{saving ? 'Posting…' : 'Post update'}</button></footer>
  </Dialog.Content></Dialog.Portal></Dialog.Root>
}

function SourceMenu({ initiatives, onChange, projects, source }: { initiatives: Initiative[]; onChange: (source: Source) => void; projects: Project[]; source?: Source }) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const normalized = query.trim().toLowerCase()
  const filteredProjects = projects.filter(entity => entity.name.toLowerCase().includes(normalized))
  const filteredInitiatives = initiatives.filter(entity => entity.name.toLowerCase().includes(normalized))
  const choose = (next: Source) => { onChange(next); setOpen(false) }
  return <DropdownMenu.Root open={open} onOpenChange={next => { setOpen(next); if (!next) setQuery('') }}><DropdownMenu.Trigger asChild><button className="pulse-source-chip" type="button">{source && <ViewGlyph color={source.entity.color} icon={source.entity.icon || (source.kind === 'project' ? 'Project' : 'Initiative')}/>}<span>{source?.entity.name ?? 'Choose source'}</span></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="start" className="pulse-menu pulse-source-menu" sideOffset={5}>
    <div className="pulse-menu-search"><input aria-label="Search projects or initiatives" autoFocus placeholder="Search…" value={query} onChange={event => setQuery(event.target.value)}/></div>
    {filteredProjects.length > 0 && <DropdownMenu.Label>Projects</DropdownMenu.Label>}{filteredProjects.map(entity => <button key={entity.id} onClick={() => choose({ kind: 'project', entity })} role="menuitem" type="button"><ViewGlyph color={entity.color} icon={entity.icon || 'Project'}/><span>{entity.name}</span>{source?.entity.id === entity.id && <Check className="pulse-menu-end" size={13}/>}</button>)}
    {filteredInitiatives.length > 0 && <DropdownMenu.Label>Initiatives</DropdownMenu.Label>}{filteredInitiatives.map(entity => <button key={entity.id} onClick={() => choose({ kind: 'initiative', entity })} role="menuitem" type="button"><ViewGlyph color={entity.color} icon={entity.icon || 'Initiative'}/><span>{entity.name}</span>{source?.entity.id === entity.id && <Check className="pulse-menu-end" size={13}/>}</button>)}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function PulseHealthMenu({ health, kind, onChange }: { health: Project['health']; kind: 'project' | 'initiative'; onChange: (health: Project['health']) => void }) {
  const { t } = useI18n()
  const label = kind === 'initiative' ? t('Set initiative health') : t('Set project health')
  const values: Project['health'][] = ['onTrack', 'atRisk', 'offTrack']
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const visible = values.filter(value => healthLabel(value).toLowerCase().includes(query.trim().toLowerCase()))
  const choose = (value: Project['health']) => { onChange(value); setOpen(false) }
  return <DropdownMenu.Root open={open} onOpenChange={next => { setOpen(next); if (!next) setQuery('') }}><DropdownMenu.Trigger asChild><button aria-label={label} className={`pulse-health-chip is-${health}`} type="button"><i/><span>{t(healthLabel(health))}</span></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="start" className="pulse-menu pulse-health-menu" sideOffset={5}><div className="pulse-menu-search"><input aria-label={label} autoFocus placeholder={`${label}…`} value={query} onChange={event => setQuery(event.target.value)}/></div>{visible.map(value => <button key={value} onClick={() => choose(value)} role="menuitem" type="button"><i className={`pulse-health-dot is-${value}`}/><span>{t(healthLabel(value))}</span>{health === value && <Check className="pulse-menu-end" size={13}/>}</button>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}


function healthLabel(value: Project['health']) { return HEALTH_LABELS[value] }
