import { useEffect, useMemo, useState, type ReactNode } from 'react'
import * as Popover from '@radix-ui/react-popover'
import { ArrowUpRight, Check, MoreHorizontal, Sparkles, X } from 'lucide-react'
import { toast } from 'sonner'
import {
  acceptIssueSuggestion,
  dismissIssueSuggestion,
  fetchIssueRecord,
  fetchIssueSuggestions,
  refreshIssueSuggestions,
} from '@/lib/api'
import { Avatar } from '@/components/issue/issue-row'
import { ProjectIcon, StatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import type { BootstrapData, Issue, IssueSuggestion } from '@/types/flow'
import './triage-intelligence-suggestions.css'

type PropertySuggestion = IssueSuggestion & {
  type: 'assignee' | 'project' | 'label' | 'team'
}

export function TriageIntelligenceSuggestions({
  issue,
  data,
  onIssueUpdated,
  maxSuggestions,
  isVisibleInTriageAccept,
}: {
  issue: Issue
  data: BootstrapData
  onIssueUpdated?: (issue: Issue) => void
  /** Cap property suggestion chips (Fast accept uses 3). */
  maxSuggestions?: number
  /** When true, marks the panel as visible in triage accept host. */
  isVisibleInTriageAccept?: boolean
}) {
  const [removed, setRemoved] = useState<Set<string>>(() => new Set())
  const [remoteSuggestions, setRemoteSuggestions] = useState<IssueSuggestion[]>()
  const [remoteGeneratedAt, setRemoteGeneratedAt] = useState<string>()
  const [busy, setBusy] = useState<string>()
  const [refreshing, setRefreshing] = useState(false)
  const enabled = data.workspaceSettings.featureFlags?.['triage-intelligence'] ?? false
  const inTriage = issue.state.type === 'backlog' && !issue.triagedAt

  useEffect(() => {
    if (!enabled || !inTriage) {
      setRemoteSuggestions(undefined)
      return
    }
    const controller = new AbortController()
    void fetchIssueSuggestions(issue.id, controller.signal)
      .then(result => {
        if (!controller.signal.aborted) {
          setRemoteSuggestions(result.suggestions)
          setRemoteGeneratedAt(result.suggestionsGeneratedAt)
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) setRemoteSuggestions(undefined)
      })
    return () => controller.abort()
  }, [enabled, inTriage, issue.id, issue.suggestionsGeneratedAt])

  const suggestions = useMemo(
    () =>
      (remoteSuggestions ?? data.issueSuggestions ?? [])
        .filter(item => item.issueId === issue.id && item.state === 'active' && !removed.has(item.id))
        .sort((left, right) => Number(left.metadata.rank ?? 0) - Number(right.metadata.rank ?? 0)),
    [data.issueSuggestions, issue.id, remoteSuggestions, removed],
  )
  const propertySuggestions = suggestions.filter(
    (item): item is PropertySuggestion =>
      item.type === 'assignee' || item.type === 'project' || item.type === 'label' || item.type === 'team',
  )
  const visiblePropertySuggestions =
    typeof maxSuggestions === 'number' ? propertySuggestions.slice(0, maxSuggestions) : propertySuggestions
  const duplicates = suggestions.filter(item => item.type === 'similarIssue')
  const related = suggestions.filter(item => item.type === 'relatedIssue')

  if (!enabled || !inTriage) return null

  const update = async (suggestion: IssueSuggestion, accept: boolean) => {
    setBusy(suggestion.id)
    try {
      if (accept) await acceptIssueSuggestion(issue.id, suggestion.id)
      else await dismissIssueSuggestion(issue.id, suggestion.id)
      if (accept) {
        const refreshed = await fetchIssueRecord(issue.id, undefined, data.workspace.urlKey)
        onIssueUpdated?.(refreshed)
      }
      setRemoteSuggestions(current => (current ?? data.issueSuggestions ?? []).filter(item => item.id !== suggestion.id))
      setRemoved(current => new Set(current).add(suggestion.id))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Could not update suggestion')
    } finally {
      setBusy(undefined)
    }
  }

  const refresh = async () => {
    setRefreshing(true)
    setRemoved(new Set())
    try {
      await refreshIssueSuggestions(issue.id)
      const result = await fetchIssueSuggestions(issue.id)
      setRemoteSuggestions(result.suggestions)
      setRemoteGeneratedAt(result.suggestionsGeneratedAt)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Could not refresh suggestions')
    } finally {
      setRefreshing(false)
    }
  }

  const thinking = !(remoteGeneratedAt ?? issue.suggestionsGeneratedAt) && !suggestions.length
  if (!thinking && !suggestions.length) return null

  return (
    <section className="triage-intelligence-panel" aria-label="Triage Intelligence" data-triage-accept={isVisibleInTriageAccept || undefined}>
      <header>
        <span className="triage-intelligence-title">
          <Sparkles size={14} />
          <strong>Triage Intelligence</strong>
        </span>
        <button
          className="triage-intelligence-refresh"
          type="button"
          aria-label="Refresh Triage Intelligence suggestions"
          disabled={refreshing}
          onClick={() => void refresh()}
        >
          {refreshing ? <span className="triage-intelligence-spinner" /> : <MoreHorizontal size={15} />}
        </button>
      </header>
      {thinking ? (
        <div className="triage-intelligence-thinking">
          <span className="triage-intelligence-pulse" />
          <span>Analyzing related work and inferring properties…</span>
        </div>
      ) : (
        <div className="triage-intelligence-body">
          {visiblePropertySuggestions.length > 0 && (
            <div className="triage-intelligence-row">
              <span className="triage-intelligence-row-label">Suggestions</span>
              <div className="triage-intelligence-chips">
                {visiblePropertySuggestions.map(suggestion => (
                  <PropertySuggestionChip
                    key={suggestion.id}
                    suggestion={suggestion}
                    data={data}
                    busy={busy === suggestion.id}
                    onAccept={() => void update(suggestion, true)}
                    onDismiss={() => void update(suggestion, false)}
                  />
                ))}
              </div>
            </div>
          )}
          {duplicates.length > 0 && (
            <div className="triage-intelligence-row">
              <span className="triage-intelligence-row-label">Duplicate of</span>
              <RelatedSuggestionList
                suggestions={duplicates}
                data={data}
                busy={busy}
                onAccept={suggestion => void update(suggestion, true)}
                onDismiss={suggestion => void update(suggestion, false)}
              />
            </div>
          )}
          {related.length > 0 && (
            <div className="triage-intelligence-row">
              <span className="triage-intelligence-row-label">Related to</span>
              <RelatedSuggestionList
                suggestions={related}
                data={data}
                busy={busy}
                onAccept={suggestion => void update(suggestion, true)}
                onDismiss={suggestion => void update(suggestion, false)}
              />
            </div>
          )}
        </div>
      )}
    </section>
  )
}

function PropertySuggestionChip({
  suggestion,
  data,
  busy,
  onAccept,
  onDismiss,
}: {
  suggestion: PropertySuggestion
  data: BootstrapData
  busy: boolean
  onAccept: () => void
  onDismiss: () => void
}) {
  const target = propertySuggestionTarget(suggestion, data)
  if (!target) return null
  const reasons = suggestion.metadata.reasons ?? []
  return (
    <Popover.Root>
      <Popover.Trigger asChild>
        <button
          className={`triage-intelligence-chip triage-intelligence-chip-${suggestion.type}`}
          type="button"
          aria-label={`${target.label}: ${target.detail}`}
        >
          {target.icon}
          <span>{target.detail}</span>
          <Check className="triage-intelligence-chip-check" size={13} />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content data-flow-motion="floating" className="triage-intelligence-popover" side="bottom" align="start" sideOffset={6} collisionPadding={12}>
          <div className="triage-intelligence-popover-target">
            {target.icon}
            <strong>{target.detail}</strong>
          </div>
          <h4>{target.whyTitle}</h4>
          {reasons.length > 0 ? (
            <ul>
              {reasons.map(reason => (
                <li key={reason}>{reason}</li>
              ))}
            </ul>
          ) : (
            <p>The workspace context points to this value.</p>
          )}
          <div className="triage-intelligence-popover-actions">
            <button type="button" disabled={busy} onClick={onAccept}>
              <Check size={14} />
              Accept suggestion
            </button>
            <button type="button" aria-label="Dismiss suggestion" disabled={busy} onClick={onDismiss}>
              <X size={14} />
            </button>
          </div>
          <Popover.Arrow className="triage-intelligence-popover-arrow" />
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}

function RelatedSuggestionList({
  suggestions,
  data,
  busy,
  onAccept,
  onDismiss,
}: {
  suggestions: IssueSuggestion[]
  data: BootstrapData
  busy: string | undefined
  onAccept: (suggestion: IssueSuggestion) => void
  onDismiss: (suggestion: IssueSuggestion) => void
}) {
  return (
    <div className="triage-intelligence-related-list">
      {suggestions.map(suggestion => {
        const relatedIssue = data.issues.find(item => item.id === suggestion.suggestedIssueId)
        if (!relatedIssue) return null
        return (
          <div className="triage-intelligence-related-row" key={suggestion.id}>
            <StatusIcon state={relatedIssue.state} size={14} />
            <a href={`/${data.workspace.urlKey}/issue/${relatedIssue.identifier}`}>
              <span>{relatedIssue.identifier}</span> {relatedIssue.title}
              <ArrowUpRight size={11} />
            </a>
            <span className="triage-intelligence-row-actions">
              <button
                type="button"
                aria-label={`Accept ${suggestion.type === 'similarIssue' ? 'duplicate' : 'related issue'}`}
                disabled={busy === suggestion.id}
                onClick={() => onAccept(suggestion)}
              >
                <Check size={13} />
              </button>
              <button
                type="button"
                aria-label={`Dismiss ${suggestion.type === 'similarIssue' ? 'duplicate' : 'related issue'}`}
                disabled={busy === suggestion.id}
                onClick={() => onDismiss(suggestion)}
              >
                <X size={13} />
              </button>
            </span>
          </div>
        )
      })}
    </div>
  )
}

function propertySuggestionTarget(
  suggestion: PropertySuggestion,
  data: BootstrapData,
): { detail: string; label: string; whyTitle: string; icon: ReactNode } | undefined {
  if (suggestion.type === 'assignee') {
    const user = data.users.find(item => item.id === suggestion.suggestedUserId)
    return user
      ? {
          detail: user.displayName,
          label: 'Assign to user',
          whyTitle: 'Why this assignee was suggested',
          icon: <Avatar name={user.displayName} />,
        }
      : undefined
  }
  if (suggestion.type === 'project') {
    const project = data.projects.find(item => item.id === suggestion.suggestedProjectId)
    return project
      ? {
          detail: project.name,
          label: 'Add to project',
          whyTitle: 'Why this project was suggested',
          icon: <ProjectIcon style={{ color: project.color }} />,
        }
      : undefined
  }
  if (suggestion.type === 'label') {
    const label = data.labels.find(item => item.id === suggestion.suggestedLabelId)
    return label
      ? {
          detail: label.name,
          label: 'Add label',
          whyTitle: 'Why this label was suggested',
          icon: <span className="triage-intelligence-chip-dot" style={{ backgroundColor: label.color }} />,
        }
      : undefined
  }
  const team = data.teams.find(item => item.id === suggestion.suggestedTeamId)
  return team
    ? {
        detail: team.name,
        label: 'Move to team',
        whyTitle: 'Why this team was suggested',
        icon: <TeamIcon team={team} />,
      }
    : undefined
}
