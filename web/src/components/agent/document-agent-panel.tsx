import { useEffect, useSyncExternalStore } from 'react'
import type { BootstrapData, FlowDocument } from '@/types/flow'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { IssueAgentTasks } from './issue-agent-tasks'
import { EntityAgentPanel, type AgentSidebarTarget } from './entity-agent-panel'
import { documentAgentChangesState } from './document-agent-changes-state'
import { useI18n } from '@/i18n/i18n'
import styles from './document-agent-panel.module.css'

export type DocumentAgentPanelProps = {
  document: FlowDocument
  data: BootstrapData
  open: boolean
  onRequestClose: () => void
  onOpenFullPage?: () => void
  agentSidebarTarget?: AgentSidebarTarget
  initialPrompt?: string
  contextIssues?: MyIssuesRowData[]
}

/**
 * LS-0210 DocumentAgentPanel — EntityAgentPanel wrapper for documents,
 * paired with DocumentAgentChangesState (LS-0209) + existing IssueAgentTasks.
 */
export function DocumentAgentPanel({
  document,
  data,
  open,
  onRequestClose,
  onOpenFullPage,
  agentSidebarTarget,
  initialPrompt,
  contextIssues = [],
}: DocumentAgentPanelProps) {
  const { t } = useI18n()
  const contentId = document.id
  const availability = useSyncExternalStore(
    listener => documentAgentChangesState.subscribe(listener),
    () => documentAgentChangesState.availabilityFor(contentId),
    () => 'cleared' as const,
  )
  const changesVisible = useSyncExternalStore(
    listener => documentAgentChangesState.subscribe(listener),
    () => documentAgentChangesState.visibleFor(contentId),
    () => false,
  )

  useEffect(() => {
    if (!open) return
    // Opening the document agent marks changes as pending until a turn completes.
    if (documentAgentChangesState.availabilityFor(contentId) === 'cleared') {
      documentAgentChangesState.setPending(contentId)
    }
  }, [contentId, open])

  return (
    <div className={styles.root} data-changes={availability} data-changes-visible={changesVisible || undefined}>
      <EntityAgentPanel
        agentSidebarTarget={agentSidebarTarget}
        contextIssues={contextIssues}
        emptyLabel={t('Ask anything or propose changes')}
        initialPrompt={initialPrompt}
        onOpenFullPage={onOpenFullPage}
        onRequestClose={onRequestClose}
        onSessionChange={() => {
          documentAgentChangesState.setActive(contentId, 'iteration')
        }}
        open={open}
        target={{
          type: 'document',
          id: document.id,
          title: document.title,
          issueIds: document.issueId ? [document.issueId] : [],
        }}
      />
      {open && (
        <div className={styles.tasks}>
          <IssueAgentTasks data={data} issue={{ id: document.id }} resourceType="document" />
        </div>
      )}
      {changesVisible && (
        <div className={styles.changesBanner} role="status">
          {t('Document agent changes are available')}
          <button
            onClick={() => documentAgentChangesState.syncEditorHighlightVisible(contentId, false)}
            type="button"
          >
            {t('Hide')}
          </button>
          <button onClick={() => documentAgentChangesState.setCleared(contentId)} type="button">
            {t('Dismiss')}
          </button>
        </div>
      )}
    </div>
  )
}
