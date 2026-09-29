import { useMemo, useState } from 'react'
import { LoaderCircle } from 'lucide-react'
import { toast } from 'sonner'
import { createComment, updateIssue } from '@/lib/api'
import { PriorityPicker, StatusPicker } from '@/components/issue/core-property-pickers'
import { TriageIntelligenceSuggestions } from '@/components/issue/triage-intelligence-suggestions'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Issue, Team } from '@/types/flow'
import { resolveAcceptState, teamStates, TRIAGE_SHORTCUTS } from './triage-model'
import './triage.css'

export type FastTriageAcceptEditorProps = {
  issue: Issue
  data: BootstrapData
  team: Team
  onAccepted: (issue: Issue) => void
  onIssueUpdated?: (issue: Issue) => void
  /** When true, Accept is blocked with upload-in-progress copy. */
  uploadInProgress?: boolean
}

/**
 * LS-0260 — FastTriageAcceptEditor.
 * `location=triageAccept`; honors team `triageRequirePriority`; comment + suggestions (max 3).
 */
export function FastTriageAcceptEditor({
  issue,
  data,
  team,
  onAccepted,
  onIssueUpdated,
  uploadInProgress = false,
}: FastTriageAcceptEditorProps) {
  const { t } = useI18n()
  const teamSettings = data.teamSettings?.[team.id]
  const requirePriority = Boolean(teamSettings?.triageRequirePriority)
  const states = useMemo(() => teamStates(data.states, team.id), [data.states, team.id])
  const defaultAcceptState = useMemo(() => resolveAcceptState(states, teamSettings?.defaultStateId), [states, teamSettings?.defaultStateId])
  const [priority, setPriority] = useState(issue.priority)
  const [stateId, setStateId] = useState(defaultAcceptState?.id ?? issue.state.id)
  const [comment, setComment] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const selectedState = states.find(state => state.id === stateId) ?? defaultAcceptState ?? issue.state
  const showPriorityWarning = requirePriority && priority === 0
  const canAccept = !uploadInProgress && !busy && !showPriorityWarning && selectedState.type !== 'backlog'

  const accept = async () => {
    if (!canAccept) return
    setBusy(true)
    setError('')
    try {
      const updated = await updateIssue(issue.id, {
        priority,
        stateId: selectedState.id,
        expectedVersion: issue.version,
      })
      const body = comment.trim()
      if (body) {
        await createComment(issue.id, body)
      }
      toast.success(t('Issue accepted'))
      onAccepted(updated)
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : t('Could not accept issue')
      setError(message)
      toast.error(message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section
      className="flow-fast-triage-accept"
      data-location="triageAccept"
      aria-label={t('Accept issue')}
    >
      <header className="flow-fast-triage-accept__header">
        <span className="flow-fast-triage-accept__id" data-i18n-ignore>
          {issue.identifier}
        </span>
        <h2 data-i18n-ignore>{issue.title}</h2>
      </header>

      <div className="flow-fast-triage-accept__properties">
        <StatusPicker
          value={selectedState}
          states={states.filter(state => state.type !== 'backlog' || state.id === selectedState.id)}
          onChange={id => setStateId(id)}
        />
        <PriorityPicker value={priority} onChange={setPriority} />
      </div>

      {showPriorityWarning ? (
        <p className="flow-fast-triage-accept__warning" role="status">
          {t('Set a priority before moving this issue out of triage.')}
        </p>
      ) : null}

      <label className="flow-fast-triage-accept__comment">
        <span>{t('Comment for accepting issue')}</span>
        <textarea
          rows={3}
          value={comment}
          placeholder={t('Add an optional comment…')}
          onChange={event => setComment(event.target.value)}
        />
      </label>

      <div className="flow-fast-triage-accept__suggestions" data-max-suggestions="3">
        <TriageIntelligenceSuggestions
          issue={{ ...issue, priority }}
          data={data}
          onIssueUpdated={onIssueUpdated}
          maxSuggestions={3}
          inTriage
          variant="compact"
          isVisibleInTriageAccept
        />
      </div>

      {error ? (
        <p className="flow-fast-triage-accept__error" role="alert">
          {error}
        </p>
      ) : null}

      <footer className="flow-fast-triage-accept__footer">
        {uploadInProgress ? (
          <span className="flow-fast-triage-accept__upload" role="status">
            <LoaderCircle className="spin" size={14} />
            {t('Upload in progress')} · {t('Please wait…')}
          </span>
        ) : null}
        <button
          type="button"
          className="flow-fast-triage-accept__submit"
          disabled={!canAccept}
          aria-keyshortcuts={TRIAGE_SHORTCUTS.accept}
          onClick={() => void accept()}
        >
          {busy ? <LoaderCircle className="spin" size={14} /> : null}
          {t('Accept issue')}
          <kbd>{TRIAGE_SHORTCUTS.accept}</kbd>
        </button>
      </footer>
    </section>
  )
}
