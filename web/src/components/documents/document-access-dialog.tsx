/** "People with access" (Flow-only): per-subject roles on a document. */
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { SelectControl } from '@/components/ui/select-control'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, DocumentPermission, FlowDocument } from '@/types/flow'

export function DocumentAccessDialog({ open, onOpenChange, data, document, permissions, busy, onChangeRole }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  data: BootstrapData
  document: FlowDocument
  permissions: DocumentPermission[]
  busy: boolean
  onChangeRole: (subjectType: string, subjectId: string, role: string) => void
}) {
  const { t } = useI18n()
  const members = data.members ?? []
  const subjects = [
    { type: 'workspace', id: data.workspace.id, label: t('Everyone in workspace'), entity: false },
    ...data.teams.map(team => ({ type: 'team', id: team.id, label: team.name, entity: true })),
    ...members.map(member => ({ type: 'user', id: member.user.id, label: member.user.displayName, entity: true })),
  ]
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="document-access">
      <DialogTitle>{t('People with access')}</DialogTitle>
      <p className="document-access-description">{t('Choose who can view, comment on, or edit this document.')}</p>
      <div className="document-access-list">
        {subjects.map(subject => {
          const role = permissions.find(item => item.subjectType === subject.type && item.subjectId === subject.id)?.role ?? (subject.type === 'user' && subject.id === document.creator.id ? 'owner' : 'none')
          return <div className="document-access-row" key={`${subject.type}:${subject.id}`}>
            <span className="document-access-person">
              {subject.type === 'user' ? <UserAvatar className="document-meta-avatar" name={subject.label}/> : <span className="document-access-team-mark">{subject.type === 'team' ? subject.label.slice(0, 1).toUpperCase() : '@'}</span>}
              <strong data-i18n-ignore={!subject.entity || undefined}>{subject.label}</strong>
            </span>
            <SelectControl
              disabled={(subject.type === 'user' && subject.id === document.creator.id) || busy}
              label={`${t('Access for')} ${subject.label}`}
              value={role}
              onChange={value => onChangeRole(subject.type, subject.id, value)}
              options={[{ value: 'none', label: t('No access') }, { value: 'viewer', label: t('Can view') }, { value: 'commenter', label: t('Can comment') }, { value: 'editor', label: t('Can edit') }, { value: 'owner', label: t('Owner'), disabled: true }]}
            />
          </div>
        })}
      </div>
    </DialogContent>
  </Dialog>
}
