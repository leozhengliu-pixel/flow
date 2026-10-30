import * as Dialog from '@radix-ui/react-dialog'
import { Flag } from 'lucide-react'
import { useState } from 'react'
import { useI18n } from '@/i18n/i18n'
import { issuePath } from '@/lib/app-routes'
import { issueRelationEntries } from '@/components/issue/issue-relation-model'
import { StatusIcon } from '@/components/issue/issue-icons'
import { useIssuesById } from '@/components/issue/use-issues-by-id'
import type { ActivityEvent, BootstrapData, Issue } from '@/types/flow'

export function IssueMobileRelations({ issue, data, activities, onOpenIssue }: { issue: Issue; data: BootstrapData; activities: ActivityEvent[]; onOpenIssue: (issue: Issue) => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const entries = issueRelationEntries(issue, data.issues, activities)
  const targets = useIssuesById(entries.map(entry => entry.targetId), data)
  if (!entries.length) return null
  const titles = { blockedBy: 'Blocked by', blocking: 'Blocking', related: 'Related', duplicateOf: 'Duplicate of', duplicates: 'Duplicates' }
  return <Dialog.Root open={open} onOpenChange={value => { setOpen(value); setQuery('') }}>
    <Dialog.Trigger asChild><button type="button" className="label-project-trigger issue-mobile-relations-trigger" aria-label={t('Open related issue')}><Flag size={16}/><span>{t('{count} related').replace('{count}', String(entries.length))}</span></button></Dialog.Trigger>
    <Dialog.Portal>
      <Dialog.Overlay className="issue-mobile-command-overlay" data-flow-motion="backdrop"/>
      <Dialog.Content className="issue-mobile-relations-menu" aria-describedby={undefined} data-flow-motion="dialog" data-dialog-placement="window">
        <Dialog.Title className="sr-only">{t('Related issues')}</Dialog.Title>
        <label className="issue-mobile-relations-search"><input aria-label={t('Search related issues')} placeholder={t('Open related issue…')} value={query} onChange={event => setQuery(event.target.value)}/></label>
        <div className="issue-mobile-relations-list">
          <h3>{t('Navigation')}</h3>
          {(['blockedBy', 'blocking', 'related', 'duplicateOf', 'duplicates'] as const).map(kind => {
            const rows = entries.filter(entry => entry.kind === kind).flatMap(entry => {
              const target = targets.get(entry.targetId)
              return target && `${target.identifier} ${target.title}`.toLowerCase().includes(query.toLowerCase()) ? [target] : []
            })
            return rows.length > 0 && <section key={kind}><h4>{t(kind==='blocking'?'Blocks':titles[kind])}</h4>{rows.map(target => <a key={target.id} href={issuePath(data.workspace.urlKey, target)} onClick={event => {
              if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
              event.preventDefault(); setOpen(false); onOpenIssue(target)
            }}><StatusIcon state={target.state} size={16}/><span data-i18n-ignore>{target.identifier}</span><strong data-i18n-ignore>{target.title}</strong></a>)}</section>
          })}
        </div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}
