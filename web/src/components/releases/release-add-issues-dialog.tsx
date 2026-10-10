import { Command } from 'cmdk'
import { Check } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'

import { StatusIcon } from '@/components/issue/issue-icons'
import { useIssueSearch } from '@/components/issue/use-issue-search'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { updateRelease } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Release } from '@/types/flow'

/**
 * Linear's "Add issues to release…" large menu: one search field ("Search for issues to add to
 * {release}…"); picking an issue toggles it in the release and keeps the menu open.
 */
export function ReleaseAddIssuesDialog({ data, release, onClose, onReload }: { data: BootstrapData; release: Release; onClose: () => void; onReload: () => Promise<void> }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [issueIds, setIssueIds] = useState(release.issueIds)
  const [busy, setBusy] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    const timer = window.setTimeout(() => inputRef.current?.focus(), 60)
    return () => window.clearTimeout(timer)
  }, [])
  const found = useIssueSearch(query, data.issues)
  const issues = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase()
    const matches = needle ? found.filter(issue => `${issue.identifier} ${issue.title}`.toLocaleLowerCase().includes(needle)) : found
    return matches.slice(0, 50)
  }, [found, query])
  const toggle = async (issueId: string) => {
    if (busy) return
    const next = issueIds.includes(issueId) ? issueIds.filter(id => id !== issueId) : [...issueIds, issueId]
    setBusy(true)
    setIssueIds(next)
    try {
      await updateRelease(release.id, { issueIds: next })
      await onReload()
    } catch (error) {
      setIssueIds(issueIds)
      toast.error(error instanceof Error ? error.message : t('Could not save release'))
    } finally { setBusy(false) }
  }
  const placeholder = t('Search for issues to add to {release}…').replace('{release}', release.name)
  return <Dialog open onOpenChange={open => { if (!open) onClose() }}>
    <DialogContent className="command-dialog flow-release-add-issues" overlayClassName="command-overlay" onOpenAutoFocus={event => { event.preventDefault(); inputRef.current?.focus() }} onCloseAutoFocus={event => event.preventDefault()}>
      <DialogTitle className="sr-only">{t('Add issues to release…')}</DialogTitle>
      <Command shouldFilter={false} loop>
        <div className="command-input">
          <Command.Input ref={inputRef} aria-label={placeholder} autoFocus placeholder={placeholder} value={query} onValueChange={setQuery}/>
        </div>
        {query.trim() && <Command.List>
          {issues.map(issue => {
            const added = issueIds.includes(issue.id)
            return <Command.Item key={issue.id} value={issue.id} onSelect={() => void toggle(issue.id)} data-i18n-ignore>
              <span className="command-item-icon"><StatusIcon state={issue.state} size={14}/></span>
              <span className="flow-release-add-issues__id">{issue.identifier}</span>
              <span className="flow-release-add-issues__title">{issue.title}</span>
              {added && <Check className="flow-release-add-issues__check" aria-label={t('Added')}/>}
            </Command.Item>
          })}
          <Command.Empty>{t('No issues found')}</Command.Empty>
        </Command.List>}
      </Command>
    </DialogContent>
  </Dialog>
}
