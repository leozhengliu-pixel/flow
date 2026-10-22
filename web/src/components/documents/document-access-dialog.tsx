/** "People with access" (Flow-only): per-subject roles on a document.
 *  Role changes are staged locally; the primary button applies them in one request, Cancel / Esc / close discards. */
import './document-access-dialog.css'
import { useLayoutEffect, useRef, useState } from 'react'
import { Description as DialogDescription } from '@radix-ui/react-dialog'
import { Dialog, DialogClose, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { SelectControl } from '@/components/ui/select-control'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, DocumentPermission, FlowDocument } from '@/types/flow'

export type DocumentAccessChange = { subjectType: string; subjectId: string; role: string }

const subjectKey = (type: string, id: string) => `${type}:${id}`

export function DocumentAccessDialog({ open, onOpenChange, data, document, permissions, onSave }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  data: BootstrapData
  document: FlowDocument
  permissions: DocumentPermission[]
  /** Apply every staged change at once. Reject to keep the dialog open with the staged choices intact. */
  onSave: (changes: DocumentAccessChange[]) => Promise<void>
}) {
  const [saving, setSaving] = useState(false)
  // Opened from a menu item (no Dialog.Trigger), so remember what had focus and give it back on close.
  const opener = useRef<HTMLElement | null>(null)
  useLayoutEffect(() => { if (open) opener.current = globalThis.document.activeElement as HTMLElement | null }, [open])
  // Esc, the close button and the overlay all discard the staged changes, except while a save is in flight.
  return <Dialog open={open} onOpenChange={next => { if (!next && saving) return; onOpenChange(next) }}>
    <DialogContent className="action-dialog document-access" onCloseAutoFocus={event => { const target = opener.current; if (target?.isConnected && target !== globalThis.document.body) { event.preventDefault(); target.focus() } }} overlayClassName="action-dialog-overlay">
      <AccessBody data={data} document={document} onClose={() => onOpenChange(false)} onSave={onSave} permissions={permissions} saving={saving} setSaving={setSaving}/>
    </DialogContent>
  </Dialog>
}

/** Mounted only while the dialog is open, so staged state always starts empty. */
function AccessBody({ data, document, permissions, onSave, onClose, saving, setSaving }: {
  data: BootstrapData
  document: FlowDocument
  permissions: DocumentPermission[]
  onSave: (changes: DocumentAccessChange[]) => Promise<void>
  onClose: () => void
  saving: boolean
  setSaving: (saving: boolean) => void
}) {
  const { t } = useI18n()
  const [staged, setStaged] = useState<Record<string, string>>({})
  const [failed, setFailed] = useState<string | null>(null)
  const members = data.members ?? []
  const subjects: Array<{ type: string; id: string; label: string; entity: boolean; email?: string }> = [
    { type: 'workspace', id: data.workspace.id, label: t('Everyone in workspace'), entity: false },
    ...data.teams.map(team => ({ type: 'team', id: team.id, label: team.name, entity: true })),
    ...members.map(member => ({ type: 'user', id: member.user.id, label: member.user.displayName, entity: true, email: member.user.email as string | undefined })),
  ]
  const savedRole = (type: string, id: string) => permissions.find(item => item.subjectType === type && item.subjectId === id)?.role ?? (type === 'user' && id === document.creator.id ? 'owner' : 'none')
  const changes: DocumentAccessChange[] = subjects.flatMap(subject => {
    const next = staged[subjectKey(subject.type, subject.id)]
    return next !== undefined && next !== savedRole(subject.type, subject.id) ? [{ subjectType: subject.type, subjectId: subject.id, role: next }] : []
  })
  const stage = (type: string, id: string, role: string) => {
    setFailed(null)
    setStaged(current => {
      const next = { ...current }
      if (role === savedRole(type, id)) delete next[subjectKey(type, id)]
      else next[subjectKey(type, id)] = role
      return next
    })
  }
  const save = async () => {
    if (saving || !changes.length) return
    setSaving(true)
    setFailed(null)
    try { await onSave(changes); onClose() }
    catch (error) { setFailed(error instanceof Error && error.message ? error.message : '') }
    finally { setSaving(false) }
  }
  return <>
    <DialogTitle>{t('People with access')}</DialogTitle>
    <DialogDescription className="document-access-description">{t('Choose who can view, comment on, or edit this document.')}</DialogDescription>
    <div className="document-access-list">
      {subjects.map(subject => {
        const key = subjectKey(subject.type, subject.id)
        const pending = key in staged
        const role = staged[key] ?? savedRole(subject.type, subject.id)
        return <div aria-busy={pending && saving ? true : undefined} className="document-access-row" data-error={failed !== null && pending ? '' : undefined} data-pending={pending ? '' : undefined} key={key}>
          <span className="document-access-person">
            {subject.type === 'user' ? <UserAvatar className="document-meta-avatar" name={subject.label}/> : <span className="document-access-team-mark">{subject.type === 'team' ? subject.label.slice(0, 1).toUpperCase() : '@'}</span>}
            <span className="document-access-text"><strong data-i18n-ignore={!subject.entity || undefined}>{subject.label}</strong>{subject.type === 'user' && subject.email ? <span data-i18n-ignore>{subject.email}</span> : null}</span>
          </span>
          {pending ? <span aria-label={t('Unsaved change')} className="document-access-pending" role="img" title={t('Unsaved change')}/> : null}
          <SelectControl
            disabled={(subject.type === 'user' && subject.id === document.creator.id) || saving}
            label={`${t('Access for')} ${subject.label}`}
            value={role}
            onChange={value => stage(subject.type, subject.id, value)}
            options={[{ value: 'none', label: t('No access') }, { value: 'viewer', label: t('Can view') }, { value: 'commenter', label: t('Can comment') }, { value: 'editor', label: t('Can edit') }, { value: 'owner', label: t('Owner'), disabled: true }]}
          />
        </div>
      })}
    </div>
    {failed !== null ? <p className="document-access-error" role="alert">{t('Could not update document access')}{failed ? <span data-i18n-ignore>{`: ${failed}`}</span> : null}</p> : null}
    <footer>
      <DialogClose asChild><button disabled={saving} type="button">{t('Cancel')}</button></DialogClose>
      <button className="primary" disabled={!changes.length || saving} onClick={() => void save()} type="button">{t('Save')}</button>
    </footer>
  </>
}
