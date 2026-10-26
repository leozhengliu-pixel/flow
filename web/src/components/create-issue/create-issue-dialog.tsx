import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent, type ReactNode, type RefObject } from 'react'
import { assigneeMenuOptions } from '@/components/issue/assignee-menu-options'
import { agentsAvailable } from '@/lib/agent-members'
import { ProjectGlyph } from '@/components/views/project-glyph'
import { estimateLabel, estimatePickerOptions, NO_ESTIMATE } from '@/lib/estimates'
import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import type { Editor } from '@tiptap/react'
import { ChevronRight, CircleDashed, Diamond, ExternalLink, Link2, Maximize2, Minimize2, MoreHorizontal, Paperclip, Repeat2, Trash2, X } from 'lucide-react'
import type { BootstrapData, Draft, Issue, IssueLabel, IssueSuggestionPreview } from '@/types/flow'
import { PropertyMenu } from '@/components/property/property-menu'
import { CalendarIcon, CycleIcon, LabelIcon, NoAssigneeIcon, NoProjectIcon, PriorityIcon, ProjectIcon, StatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import { Avatar } from '@/components/issue/issue-row'
import { IssueTitleEditor } from '@/components/issue/issue-title-editor'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import type { DescriptionSnapshot } from '@/components/issue/editor/editor-content'
import { DueDateCommand } from '@/components/issue/due-date-picker'
import styles from './create-issue-dialog.module.css'
import { createDraft, createRelation, deleteDraft, fetchIssueSuggestionPreview, updateDraft, updateIssue } from '@/lib/api'
import { labelTeamScopeIds, labelsForResource, toggleGroupedLabelIds } from '@/lib/labels'
import { resolvedTeamSettings } from '@/lib/team-hierarchy'
import { AttachmentRemoveButton } from '@/components/ui/attachment-remove-button'
import { Toggle } from '@/components/ui/toggle'
import type { MyIssuesCreateContext } from '@/components/my-issues/my-issues-list'
import { RecurrenceDialog } from '@/components/issue/recurrence-picker'
import { useRecurrenceSummary } from '@/components/issue/use-recurrence-summary'
import { useI18n } from '@/i18n/i18n'
import { defaultRecurrenceFirstDue, recurrenceDate, recurrencePresetOptions, toDateInput } from '@/lib/recurrence'
import { AiBurstGlyph } from '@/components/ui/agent-glyph'
import { teamContentAccessible } from '@/lib/team-access'
import { teamDefaultState, teamForWorkflowState, teamHasWorkflowState, teamStateIdFor, teamWorkflowStates } from '@/lib/team-workflow'

export interface CreateIssueInput {
  title: string
  description: string
  descriptionState?: string
  descriptionData?: Record<string, unknown>
  contentState?: string
  stateId?: string
  priority?: number
  estimate?: number
  assigneeId?: string
  projectId?: string
  projectMilestoneId?: string
  cycleId?: string
  dueDate?: string
  labelIds?: string[]
  templateId?: string
  /** Preset or RRULE subset (see lib/recurrence); '' does not repeat. */
  recurrence?: string
  /** Legacy alias of the first due date; recurring issues now send `dueDate`. */
  nextOccurrenceAt?: string
  teamId?: string
  parentId?: string
  createMore?: boolean
}

export interface CreateIssueDialogProps {
  open: boolean
  data: BootstrapData
  initialStateId?: string
  initialProjectId?: string
  initialProjectMilestoneId?: string
  initialTeamId?: string
  initialTemplateId?: string
  initialContext?: MyIssuesCreateContext
  draftId?: string
  onOpenChange: (open: boolean) => void
  onCreate: (input: CreateIssueInput) => Promise<Issue>
  onCreateLabel?: (name: string, groupId?: string) => Promise<IssueLabel>
  onUpload?: (issueId: string, file: File) => Promise<void>
  onDraftSaved?: (draft: Draft) => Promise<void> | void
  onDraftDeleted?: (draftId: string) => Promise<void> | void
}

const priorityNames = ['No priority', 'Urgent', 'High', 'Medium', 'Low']
const draftStoragePrefix = 'flow:create-issue-draft:'

/** Teams new issues can go to: a workspace admin outside a private team manages it but cannot add issues. */
function issueTeams(data: BootstrapData) {
  return data.teams.filter(item => !item.retiredAt && teamContentAccessible(data, item))
}
const defaultIssueTeamId = (data: BootstrapData) => issueTeams(data)[0]?.id || data.teams[0]?.id || ''

interface StoredIssueDraft {
  title: string
  teamId: string
  description: DescriptionSnapshot | null
  stateId: string
  priority: number
  estimate: number
  assigneeId: string
  projectId: string
  projectMilestoneId: string
  cycleId: string
  dueDate: string
  labelIds: string[]
  templateId?: string
  recurrence?: string
  /** Legacy drafts: the first occurrence, now stored as dueDate. */
  recurrenceStart?: string
  updatedAt?: string
}

/** The statuses an automatic status asked for, in order, or a status the user picked (which is never replaced by a request). */
type StatusRequest = { candidates: (string | undefined)[]; picked: boolean }
function statusRequest(...candidates: (string | undefined)[]): StatusRequest { return { candidates, picked: false } }

export function CreateIssueDialog({ data, draftId, initialContext, initialProjectId, initialProjectMilestoneId, initialTeamId, initialStateId, initialTemplateId, onCreate, onCreateLabel, onDraftDeleted, onDraftSaved, onOpenChange, onUpload, open }: CreateIssueDialogProps) {
  const { t } = useI18n()
  const requestedTeamId = initialContext?.teamId ?? initialTeamId
  const requestedStateId = initialContext?.stateId ?? initialStateId
  // A status preset without a team (a status group's "+") opens in a team whose workflow has that status.
  const fallbackTeamId = teamForWorkflowState(data, issueTeams(data).map(item => item.id), requestedStateId) ?? defaultIssueTeamId(data)
  const [teamId, setTeamId] = useState(requestedTeamId || fallbackTeamId)
  // Only the team's effective workflow is offered or sent; the API rejects any other status.
  const availableStates = useMemo(() => teamWorkflowStates({ states: data.states, teamSettings: data.teamSettings }, teamId), [data.states, data.teamSettings, teamId])
  const defaultState = useMemo(() => teamDefaultState({ states: data.states, teamSettings: data.teamSettings }, teamId, availableStates), [availableStates, data.states, data.teamSettings, teamId])
  const [title, setTitle] = useState(initialContext?.title ?? '')
  const [description, setDescription] = useState<DescriptionSnapshot | null>(null)
  const [stateId, setStateId] = useState(() => teamStateIdFor(data, requestedTeamId || fallbackTeamId, requestedStateId))
  // Where the status came from: a status the user picked is theirs; an automatic one (the explicit, preset,
  // template or draft request, else the team default) is re-applied once the team's statuses can take it.
  const statusRequestRef = useRef<StatusRequest>(statusRequest(requestedStateId))
  const pickStatus = (id: string) => { statusRequestRef.current = { candidates: [], picked: true }; setStateId(id) }
  const [priority, setPriority] = useState<number>(initialContext?.priority ?? 0)
  const [estimate, setEstimate] = useState(NO_ESTIMATE)
  const [assigneeId, setAssigneeId] = useState(initialContext?.assigneeId ?? data.viewer.id)
  const [projectId, setProjectId] = useState(initialContext?.projectId ?? '')
  const [projectMilestoneId, setProjectMilestoneId] = useState(initialContext?.projectMilestoneId ?? '')
  const [cycleId, setCycleId] = useState(initialContext?.cycleId ?? '')
  const [dueDate, setDueDate] = useState('')
  const [labelIds, setLabelIds] = useState<string[]>(initialContext?.labelIds ?? [])
  const [templateId, setTemplateId] = useState('')
  const [formValues, setFormValues] = useState<TemplateFormValues>({})
  const [invalidFields, setInvalidFields] = useState<Set<string>>(new Set())
  // What the last applied template filled in, so switching templates replaces it but never the user's own text.
  const appliedTemplateRef = useRef<{ title: string; body: string }>({ title: '', body: '' })
  const [recurrence, setRecurrence] = useState('')
  const [serverDraftId, setServerDraftId] = useState('')
  const [createMore, setCreateMore] = useState(false)
  const [expanded, setExpanded] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const [files, setFiles] = useState<File[]>([])
  const [quickSuggestions, setQuickSuggestions] = useState<IssueSuggestionPreview[]>([])
  const [quickSuggestionsLoading, setQuickSuggestionsLoading] = useState(false)
  const [linkOpen, setLinkOpen] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [discardConfirmOpen, setDiscardConfirmOpen] = useState(false)
  const titleEditorRef = useRef<Editor | null>(null)
  const descriptionEditorRef = useRef<Editor | null>(null)
  const fileRef = useRef<HTMLInputElement>(null)
  const initializationKey = [open, draftId ?? '', initialProjectId ?? '', initialProjectMilestoneId ?? '', requestedStateId ?? '', requestedTeamId ?? '', JSON.stringify(initialContext ?? {})].join('\u001f')
  const initializedKeyRef = useRef('')
  const draftKey = `${draftStoragePrefix}${teamId || data.teams[0]?.key || 'default'}`
  useEffect(()=>{if(!open||!initialTemplateId)return;const template=data.issueTemplates.find(item=>item.id===initialTemplateId);if(!template)return;setTemplateId(template.id);setTitle(current=>current||template.title||template.name);statusRequestRef.current=statusRequest(template.stateId);setStateId(template.stateId||defaultState?.id||'');setPriority(template.priority);setAssigneeId(template.assigneeId??data.viewer.id);setProjectId(template.projectId??'');setLabelIds(template.labelIds);const templateBody=template.body;if(templateBody)requestAnimationFrame(()=>descriptionEditorRef.current?.commands.setContent(templateBody,{contentType:'markdown'}))},[data.issueTemplates,data.viewer.id,defaultState?.id,initialTemplateId,open])
  const hasDraftContent = Boolean(title.trim() || description?.markdown.trim() || files.length)

  useEffect(() => {
    if (!open || (draftId && !draftId.startsWith('local:')) || !hasDraftContent) return
    const timer = window.setTimeout(() => writeStoredDraft(draftKey, {
      title, description, stateId, priority, estimate, assigneeId, projectId,
      projectMilestoneId, cycleId, dueDate, labelIds, templateId, recurrence, teamId,
    }), 250)
    return () => window.clearTimeout(timer)
  }, [assigneeId, cycleId, description, draftId, draftKey, dueDate, estimate, hasDraftContent, labelIds, open, priority, projectId, projectMilestoneId, recurrence, stateId, teamId, templateId, title])

  useEffect(() => {
    if (!open) {
      initializedKeyRef.current = ''
      return
    }
    if (initializedKeyRef.current === initializationKey) return
    initializedKeyRef.current = initializationKey
    // A new issue action must always start clean. Drafts are restored only when
    // the user explicitly resumes one from the drafts page (local or remote).
    const draft = draftId?.startsWith('local:') ? readStoredDraft(draftKey) : null
    const remoteDraft = draftId && !draftId.startsWith('local:')
      ? data.drafts.find(item => item.id === draftId)
      : undefined
    const remote = remoteDraft?.metadata as Partial<StoredIssueDraft> | undefined
    const requestedProject = initialProjectId ? data.projects.find(project => project.id === initialProjectId) : undefined
    const requestedMilestone = requestedProject?.milestones.find(milestone => milestone.id === initialProjectMilestoneId)
    const workflow = { states: data.states, teamSettings: data.teamSettings }
    if (draft || remote) {
      const restored = draft ?? remote as StoredIssueDraft
      const restoredTeamId = restored.teamId || (remoteDraft?.metadata?.teamId as string | undefined) || fallbackTeamId
      setTeamId(restoredTeamId)
      setTitle(restored.title ?? remoteDraft?.title ?? '')
      setDescription(restored.description ?? null)
      statusRequestRef.current = statusRequest(restored.stateId, requestedStateId)
      setStateId(teamStateIdFor(workflow, restoredTeamId, restored.stateId, requestedStateId))
      setPriority(restored.priority ?? initialContext?.priority ?? 0)
      setEstimate(restored.estimate ?? NO_ESTIMATE)
      setAssigneeId(restored.assigneeId ?? initialContext?.assigneeId ?? data.viewer.id)
      setProjectId(restored.projectId ?? initialContext?.projectId ?? '')
      setProjectMilestoneId(restored.projectMilestoneId ?? initialContext?.projectMilestoneId ?? '')
      setCycleId(restored.cycleId ?? initialContext?.cycleId ?? '')
      setDueDate(restored.dueDate || (restored.recurrence && restored.recurrenceStart) || '')
      setLabelIds(restored.labelIds ?? initialContext?.labelIds ?? [])
      setTemplateId(restored.templateId ?? '')
      setRecurrence(restored.recurrence ?? '')
      setServerDraftId(remoteDraft?.id ?? '')
    } else {
      setTitle(initialContext?.title ?? ''); setDescription(initialContext?.description ? {markdown:initialContext.description,document:{type:'doc',content:[{type:'paragraph',content:[{type:'text',text:initialContext.description}]}]},documentJSON:JSON.stringify({type:'doc',content:[{type:'paragraph',content:[{type:'text',text:initialContext.description}]}]}),contentState:''} : null); setFiles([]); setError(undefined); setServerDraftId(''); setTeamId(requestedTeamId || fallbackTeamId)
      statusRequestRef.current = statusRequest(requestedStateId)
      setStateId(teamStateIdFor(workflow, requestedTeamId || fallbackTeamId, requestedStateId)); setPriority(initialContext?.priority ?? 0); setEstimate(initialContext?.estimate ?? NO_ESTIMATE); setAssigneeId(initialContext?.assigneeId ?? data.viewer.id); setProjectId(initialContext?.projectId ?? ''); setProjectMilestoneId(initialContext?.projectMilestoneId ?? ''); setCycleId(initialContext?.cycleId ?? ''); setDueDate(initialContext?.dueDate ?? ''); setLabelIds(initialContext?.labelIds ?? []); setTemplateId(initialContext?.templateId ?? ''); setTemplateId(''); setRecurrence(''); setCreateMore(false); setExpanded(false)
      const textDocument = (text: string) => ({type:'doc',content:[{type:'paragraph',...(text ? {content:[{type:'text',text}]} : {})}]})
      titleEditorRef.current?.commands.setContent(textDocument(initialContext?.title ?? '')); descriptionEditorRef.current?.commands.setContent(textDocument(initialContext?.description ?? ''))
    }
    if (requestedProject) {
      setProjectId(requestedProject.id)
      setProjectMilestoneId(requestedMilestone?.id ?? '')
    }
    const frame = requestAnimationFrame(() => requestAnimationFrame(() => titleEditorRef.current?.commands.focus('end')))
    return () => cancelAnimationFrame(frame)
  }, [data.drafts, data.projects, data.states, data.teamSettings, data.teams, data.viewer.id, draftId, draftKey, fallbackTeamId, initialContext, initialProjectId, initialProjectMilestoneId, initializationKey, open, requestedStateId, requestedTeamId, teamId])

  // Every status change (team switch, template, draft, data refresh) settles on a status of
  // the team's workflow: a status it still has is kept, any other becomes the team default.
  // Until the user picks one, a requested status the team could not take yet (its own
  // statuses loaded after the workspace ones) is restored as soon as the workflow has it.
  useEffect(() => {
    if (!open || !availableStates.length) return
    const request = statusRequestRef.current
    const requested = request.picked ? undefined : request.candidates.find(id => id && availableStates.some(state => state.id === id))
    if (requested && requested !== stateId) setStateId(requested)
    else if (!requested && !availableStates.some(state => state.id === stateId)) setStateId(defaultState?.id ?? '')
  }, [availableStates, defaultState?.id, open, stateId])

  useEffect(() => {
    const query = title.trim()
    if (!open || !(data.workspaceSettings.featureFlags?.['triage-intelligence'] ?? false) || query.length < 3 || !teamId) {
      setQuickSuggestions([])
      setQuickSuggestionsLoading(false)
      return
    }
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      setQuickSuggestionsLoading(true)
      void fetchIssueSuggestionPreview(`${query}\n${description?.markdown ?? ''}`, teamId, controller.signal)
        .then(result => {
          if (!controller.signal.aborted) setQuickSuggestions(result.suggestions)
        })
        .catch(() => {
          if (!controller.signal.aborted) setQuickSuggestions([])
        })
        .finally(() => {
          if (!controller.signal.aborted) setQuickSuggestionsLoading(false)
        })
    }, 360)
    return () => {
      controller.abort()
      window.clearTimeout(timer)
    }
  }, [data.workspaceSettings.featureFlags, description?.markdown, open, teamId, title])

  const state = availableStates.find(item => item.id === stateId) ?? defaultState
  // A backlog status of a triage team files the issue into Triage, which the reference app shows as the Triage status.
  const inTriage = Boolean(data.teamSettings?.[teamId]?.triageEnabled && state?.type === 'backlog')
  const team = data.teams.find(item => item.id === teamId) ?? data.teams[0]
  const assignee = data.users.find(user => user.id === assigneeId)
  const project = data.projects.find(item => item.id === projectId)
  const projectMilestone = project?.milestones.find(item => item.id === projectMilestoneId)
  const cycle = data.cycles.find(item => item.id === cycleId)
  const cycles = data.cycles.filter(item => item.teamId === teamId && item.status !== 'completed')
  const nextUpcomingCycleId = [...cycles].filter(item => item.status === 'upcoming').sort((left,right)=>left.startsAt.localeCompare(right.startsAt))[0]?.id
  const estimateType = resolvedTeamSettings(data.teamSettings, teamId)?.estimateType ?? 'notUsed'
  const estimateOptions = estimatePickerOptions(resolvedTeamSettings(data.teamSettings, teamId))
  const issueLabels = useMemo(() => labelsForResource(data.labels, 'issue', data.labelGroups), [data.labelGroups, data.labels])
  const availableLabels = useMemo(() => {
    const scopes = new Set(labelTeamScopeIds(teamId, data.teams, data.teamSettings))
    return issueLabels.filter(label => !label.scope || label.scope === 'Workspace' || scopes.has(label.scope))
  }, [data.teamSettings, data.teams, issueLabels, teamId])
  const templateOptions = useMemo(() => issueTemplatesForTeam(data, teamId), [data, teamId])
  const selectedTemplate = templateOptions.find(item => item.id === templateId) ?? data.issueTemplates.find(item => item.id === templateId)
  const formTemplate = selectedTemplate?.templateType === 'customForm' && (selectedTemplate.formFields?.length ?? 0) > 0 ? selectedTemplate : undefined
  const availableLabelIds = useMemo(() => new Set(availableLabels.map(label => label.id)), [availableLabels])
  const applyTemplate = (id: string) => {
    const previous = appliedTemplateRef.current
    setTemplateId(id)
    setFormValues({})
    setInvalidFields(new Set())
    const template = data.issueTemplates.find(item => item.id === id)
    if (!template) {
      // "No template": take back only what the previous template filled in.
      setTitle(current => current === previous.title ? '' : current)
      if (previous.body && description?.markdown.trim() === previous.body.trim()) descriptionEditorRef.current?.commands.clearContent(true)
      appliedTemplateRef.current = { title: '', body: '' }
      return
    }
    // A workspace template's team property moves the issue to that team.
    const nextTeamId = isWorkspaceIssueTemplate(template) && template.teamId && data.teams.some(team => team.id === template.teamId) ? template.teamId : teamId
    if (nextTeamId !== teamId) { setTeamId(nextTeamId); setCycleId('') }
    const nextTitle = template.title ?? ''
    setTitle(current => !current.trim() || current === previous.title ? nextTitle : current)
    statusRequestRef.current = statusRequest(template.stateId)
    setStateId(teamStateIdFor(data, nextTeamId, template.stateId))
    setPriority(template.priority)
    setAssigneeId(template.assigneeId ?? data.viewer.id)
    setProjectId(template.projectId ?? '')
    setLabelIds(template.labelIds)
    const body = template.body ?? ''
    const currentBody = description?.markdown.trim() ?? ''
    if (!currentBody || currentBody === previous.body.trim()) {
      if (body) descriptionEditorRef.current?.commands.setContent(body, { contentType: 'markdown' })
      else descriptionEditorRef.current?.commands.clearContent(true)
    }
    appliedTemplateRef.current = { title: nextTitle, body }
  }
  // A team's default template is preselected for new, untouched issues; team
  // members and people outside the team can have different defaults.
  const teamDefaults = resolvedTeamSettings(data.teamSettings, teamId)
  const isTeamMember = data.teamMembers.some(member => member.teamId === teamId && member.userId === data.viewer.id)
  const defaultTemplateId = (isTeamMember ? teamDefaults?.defaultIssueTemplateForMembersId : teamDefaults?.defaultIssueTemplateForNonMembersId) ?? ''
  useEffect(() => {
    if (!open || initialTemplateId || initialContext?.templateId || templateId || title.trim() || description?.markdown?.trim()) return
    if (defaultTemplateId && templateOptions.some(item => item.id === defaultTemplateId)) applyTemplate(defaultTemplateId)
    // Only re-evaluate when the dialog opens or the team changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, teamId, defaultTemplateId])
  const selectedLabels = availableLabels.filter(label => labelIds.includes(label.id))
  const labelGroupNames = useMemo(() => new Map(data.labelGroups.map(group => [group.id, group.name])), [data.labelGroups])
  const labelGroupColors = useMemo(() => new Map(data.labelGroups.map(group => [group.id, group.color])), [data.labelGroups])
  const toggleLabel = (id: string) => setLabelIds(current => toggleGroupedLabelIds(current, id, availableLabels))
  const applyQuickSuggestion = (suggestion: IssueSuggestionPreview) => {
    if (suggestion.type === 'assignee' && data.users.some(user => user.id === suggestion.id)) {
      setAssigneeId(suggestion.id)
    } else if (suggestion.type === 'project' && data.projects.some(item => item.id === suggestion.id)) {
      setProjectId(suggestion.id)
      setProjectMilestoneId('')
    } else if (suggestion.type === 'label' && availableLabelIds.has(suggestion.id)) {
      setLabelIds(current => current.includes(suggestion.id) ? current : [...current, suggestion.id])
    } else if (suggestion.type === 'team' && data.teams.some(team => team.id === suggestion.id)) {
      setTeamId(suggestion.id)
      setCycleId('')
    } else {
      return
    }
    setQuickSuggestions(current => current.filter(item => item.id !== suggestion.id || item.type !== suggestion.type))
  }

  const resetBody = (focus = true) => {
    setTitle('')
    setDescription(null)
    titleEditorRef.current?.commands.clearContent()
    descriptionEditorRef.current?.commands.clearContent()
    setFiles([])
    setError(undefined)
    if (focus) requestAnimationFrame(() => titleEditorRef.current?.commands.focus('end'))
  }

  const resetDraft = () => {
    resetBody(false)
    setServerDraftId('')
    statusRequestRef.current = statusRequest()
    setStateId(defaultState?.id ?? '')
    setPriority(0)
    setEstimate(0)
    setAssigneeId(data.viewer.id)
    setProjectId('')
    setProjectMilestoneId('')
    setCycleId('')
    setDueDate('')
    setLabelIds([])
    setTemplateId('')
    setRecurrence('')
    setCreateMore(false)
    setExpanded(false)
  }

  const saveDraft = async () => {
    if (!hasDraftContent) return
    const metadata = { title, description, stateId, priority, estimate, assigneeId, projectId, projectMilestoneId, cycleId, dueDate, labelIds, templateId, recurrence, teamId }
    setSaving(true)
    setError(undefined)
    try {
      const input = { type: 'issue', title: title || 'Untitled issue', body: description?.markdown ?? '', contentData: description?.document as Record<string,unknown>|undefined, metadata }
      const saved = serverDraftId ? await updateDraft(serverDraftId, input) : await createDraft(input)
      setServerDraftId(saved.id)
      await onDraftSaved?.(saved)
      removeStoredDraft(draftKey)
      setConfirmOpen(false)
      onOpenChange(false)
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : t('Could not save draft'))
    } finally {
      setSaving(false)
    }
  }

  const discardDraft = async () => {
    removeStoredDraft(draftKey)
    if (serverDraftId) {
      await deleteDraft(serverDraftId)
      await onDraftDeleted?.(serverDraftId)
    }
    setConfirmOpen(false)
    resetDraft()
    onOpenChange(false)
  }

  const submit = useCallback(async () => {
    const cleanTitle = title.trim()
    if (formTemplate) {
      const missing = missingRequiredFields(formTemplate.formFields ?? [], formValues, { title, priority, dueDate })
      setInvalidFields(new Set(missing))
      if (missing.length) { setError(t('Fill in the required fields')); return }
    }
    if (!cleanTitle || saving) return
    // Never send a team/status pair the API would reject.
    if (!teamHasWorkflowState(data, teamId, stateId)) {
      const teamName = data.teams.find(item => item.id === teamId)?.name
      setError(!data.states.length
        ? t('Statuses are still loading. Try again in a moment.')
        : !teamName ? t(availableStates.length ? 'Choose a status from the team workflow' : 'This team has no status for new issues')
        : t(availableStates.length ? 'Choose a status from the {team} workflow' : '{team} has no status for new issues').replace('{team}', teamName))
      return
    }
    setSaving(true)
    setError(undefined)
    try {
      const issue = await onCreate({
        title: cleanTitle,
        teamId,
        description: formTemplate ? templateFormDescription(formTemplate.formFields ?? [], formValues) : description?.markdown.trim() ?? '',
        descriptionState: formTemplate ? undefined : description?.documentJSON,
        descriptionData: formTemplate ? undefined : description?.document as Record<string, unknown> | undefined,
        contentState: formTemplate ? undefined : description?.contentState,
        stateId,
        priority,
        estimate,
        assigneeId,
        projectId,
        projectMilestoneId,
        cycleId,
        dueDate,
        labelIds: labelIds.filter(id => availableLabelIds.has(id)),
        templateId,
        recurrence,
        parentId: initialContext?.parentId,
        createMore,
      })
      if (initialContext?.related) await relateCreatedIssue(issue, initialContext.related)
      const uploads = onUpload ? await Promise.allSettled(files.map(file => onUpload(issue.id, file))) : []
      const failedUploads = uploads.filter(result => result.status === 'rejected').length
      removeStoredDraft(draftKey)
      if (serverDraftId) {
        await deleteDraft(serverDraftId)
        await onDraftDeleted?.(serverDraftId)
      }
      resetBody()
      if (failedUploads) setError(t(failedUploads === 1 ? 'Issue created, but 1 attachment failed to upload' : 'Issue created, but {count} attachments failed to upload').replace('{count}', String(failedUploads)))
      if (!createMore) onOpenChange(false)
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : t('Could not create issue'))
    } finally {
      setSaving(false)
    }
  }, [assigneeId, availableLabelIds, availableStates.length, createMore, data, t, initialContext?.parentId, initialContext?.related, cycleId, description, draftKey, dueDate, estimate, files, formTemplate, formValues, labelIds, onCreate, onDraftDeleted, onOpenChange, onUpload, priority, projectId, projectMilestoneId, recurrence, saving, serverDraftId, stateId, teamId, templateId, title])

  const changeOpen = (next: boolean) => {
    if (!next && serverDraftId && hasDraftContent) {
      void saveDraft()
      return
    }
    if (!next && hasDraftContent) {
      setConfirmOpen(true)
      return
    }
    onOpenChange(next)
  }

  const addFiles = (event: ChangeEvent<HTMLInputElement>) => {
    const next = Array.from(event.target.files ?? [])
    setFiles(current => [...current, ...next])
    event.target.value = ''
  }

  const createMotionRef = useCreateMotion(open);
  return <Dialog.Root open={open} onOpenChange={changeOpen}>
    <Dialog.Portal>
      <Dialog.Overlay data-flow-motion="backdrop" className={styles.overlay} onPointerDown={() => changeOpen(false)}/>
      <Dialog.Content data-flow-motion="create" ref={createMotionRef} className={styles.dialog} data-expanded={expanded} aria-label="Create issue" onPointerDownOutside={event => event.preventDefault()} onInteractOutside={event => event.preventDefault()} onEscapeKeyDown={event => { if (saving) event.preventDefault() }}>
        <form onSubmit={event => { event.preventDefault(); void submit() }}>
          <header className={styles.header}>
            <PropertyMenu compact label="Team" value={team?.key ?? 'Team'} selectedId={teamId} options={issueTeams(data).map(item=>({id:item.id,label:item.name,keywords:item.key,icon:<TeamIcon team={item}/>,i18nIgnore:true}))} trigger={<><TeamIcon team={team}/><span data-i18n-ignore>{team?.key ?? 'Team'}</span></>} triggerClassName={styles.team} ariaLabel="Set team" onChange={id=>{setTeamId(id);setCycleId('');setLabelIds([]);setTemplateId('')}}/>
            {templateOptions.length > 0 && <PropertyMenu compact label="Template" ariaLabel="Choose a template" searchPlaceholder="Choose a template…" value={selectedTemplate?.name ?? 'Template'} valueIsEntityName={Boolean(selectedTemplate)} selectedId={templateId} triggerClassName={styles.team} trigger={<><TemplateGlyph name="page" size={13}/><span data-i18n-ignore={selectedTemplate ? true : undefined}>{selectedTemplate?.name ?? 'Template'}</span></>} options={[{ id: '', label: 'No template', icon: <TemplateGlyph name="page"/> }, ...templateOptions.map(item => ({ id: item.id, label: item.name, description: item.description, groupLabel: isWorkspaceIssueTemplate(item) ? 'Workspace' : data.teams.find(team => team.id === (item.visibilityTeamId || item.teamId))?.name, icon: <TemplateGlyph name="page"/>, i18nIgnore: true }))]} onChange={id => applyTemplate(id)}/>}
            <span className={styles.breadcrumb}>›</span><Dialog.Title>New issue</Dialog.Title>
            {serverDraftId ? <button type="button" className={styles.saveDraft} aria-label="Discard draft" disabled={saving} onClick={() => setDiscardConfirmOpen(true)}><Trash2/></button> : hasDraftContent && <button type="button" className={styles.saveDraft} aria-label="Save draft" disabled={saving} onClick={() => void saveDraft()}>{saving ? 'Saving...' : 'Save as draft'}</button>}
            <button type="button" className={`${styles.iconButton} ${styles.expandButton}`} aria-label={expanded ? 'Collapse' : 'Expand'} aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>{expanded ? <Minimize2/> : <Maximize2/>}</button>
            <Dialog.Close asChild><button type="button" className={styles.iconButton} aria-label="Close"><X/></button></Dialog.Close>
          </header>

          <div className={styles.document}>
            <IssueTitleEditor value={title} onChange={setTitle} onEnter={() => descriptionEditorRef.current?.commands.focus('start')} onSubmit={() => void submit()} autoFocus={open} className={styles.titleField} editorRef={editor => { titleEditorRef.current = editor }}/>
            {formTemplate
              ? <TemplateFormFill fields={formTemplate.formFields ?? []} values={formValues} onChange={setFormValues} title={title} onTitle={setTitle} priority={priority} onPriority={setPriority} dueDate={dueDate} onDueDate={setDueDate} invalid={invalidFields}/>
              : <IssueDescriptionEditor users={data.users} value={description?.markdown ?? ''} state={description?.documentJSON} onChange={setDescription} onSubmit={() => void submit()} className={styles.descriptionField} editorRef={editor => { descriptionEditorRef.current = editor }}/>}
          </div>

          <div className={styles.properties}>
            <MiniProperty label="Status" value={inTriage ? TRIAGE_STATUS.name : state?.name ?? 'Status'} selectedId={stateId} icon={inTriage || state ? <StatusIcon state={inTriage || !state ? TRIAGE_STATUS : state}/> : <CircleDashed aria-hidden="true"/>} options={[...availableStates].sort((a,b) => (a.position??0)-(b.position??0)).map((item,index) => ({ id:item.id,label:item.name,color:item.color,icon:<StatusIcon state={item}/>,shortcut:index < 5 ? String(index+1) : undefined }))} onChange={pickStatus}/>
            <MiniProperty label="Priority" value={priority ? priorityNames[priority] : 'Priority'} selectedId={String(priority)} icon={<PriorityIcon priority={priority}/>} options={[0,1,2,3,4].map(item => ({ id:String(item),label:priorityNames[item],icon:<PriorityIcon priority={item}/>,shortcut:String(item) }))} onChange={value => setPriority(Number(value))}/>
            {estimateType!=='notUsed'&&<MiniProperty label="Estimate" value={estimate >= 0 ? estimateLabel(estimate, estimateType) : 'Estimate'} selectedId={String(estimate)} icon={<EstimateGlyph value={Math.max(estimate, 0)}/>} options={estimateOptions.map(option=>({id:option.id,label:option.label,icon:<EstimateGlyph value={Math.max(option.value, 0)}/>}))} onChange={value=>setEstimate(Number(value))}/>}
            <MiniProperty label="Assignee" value={assignee?.displayName ?? 'Assignee'} selectedId={assigneeId} icon={assignee ? <Avatar name={assignee.displayName}/> : <NoAssigneeIcon/>} options={assigneeMenuOptions(data.users.filter(user => user.active), { teamId, allowAgents: agentsAvailable(data) })} searchPlaceholder="Assign to…" onChange={setAssigneeId}/>
            <MiniProperty label="Project" value={project?.name ?? 'Project'} valueIsEntityName={Boolean(project)} selectedId={projectId} icon={project ? <ProjectGlyph project={project}/> : <ProjectIcon/>} options={[{id:'',label:'No project',icon:<NoProjectIcon/>},...data.projects.map(item => ({id:item.id,label:item.name,color:item.color,icon:<ProjectGlyph project={item}/>,i18nIgnore:true }))]} onChange={value => { setProjectId(value); setProjectMilestoneId('') }}/>
            {project && project.milestones.length > 0 && <MiniProperty label="Milestone" value={projectMilestone?.name ?? 'Milestone'} valueIsEntityName={Boolean(projectMilestone)} selectedId={projectMilestoneId} icon={<Diamond size={14}/>} options={[{id:'',label:'No milestone',icon:<Diamond size={14}/>},...project.milestones.map(item => ({id:item.id,label:item.name,icon:<Diamond size={14}/>,i18nIgnore:true}))]} onChange={setProjectMilestoneId}/>}
            <MiniProperty multiple label="Labels" value={selectedLabels.length ? selectedLabels.map(label => label.name).join(', ') : 'Labels'} selectedIds={labelIds} icon={<LabelIcon/>} options={availableLabels.map(item => ({ id: item.id, label: item.name, color: item.color, description: item.description, issueCount: item.issueCount, scope: item.scope, resourceType: item.resourceType, groupId: item.groupId, groupLabel: item.groupId ? labelGroupNames.get(item.groupId) : undefined, groupColor: item.groupId ? labelGroupColors.get(item.groupId) : undefined }))} onChange={toggleLabel} emptyLabel="Start typing to create a new label" onCreate={onCreateLabel ? async (name, groupId) => { const label = await onCreateLabel(name, groupId); setLabelIds(current => toggleGroupedLabelIds(current, label.id, [...availableLabels, label])) } : undefined}/>
            {data.cycleSettings[teamId]?.enabled === true && <MiniProperty label="Cycle" value={cycle?.name ?? 'Cycle'} valueIsEntityName={Boolean(cycle)} selectedId={cycleId} icon={<CycleIcon cycle={cycle} nextUpcomingId={nextUpcomingCycleId} progress={cycle?cycleIssueProgress(data.issues,cycle.id):0}/>} options={[{id:'',label:'No cycle',icon:<CycleIcon noCycle/>},...cycles.map(item=>({id:item.id,label:item.name,icon:<CycleIcon cycle={item} nextUpcomingId={nextUpcomingCycleId} progress={cycleIssueProgress(data.issues,item.id)}/>,i18nIgnore:true}))]} onChange={setCycleId} ariaLabel="Add to cycle"/>}
            <MoreActions active={open && !linkOpen} dueDate={dueDate} recurrence={recurrence} onDueDateChange={setDueDate} onRecurrenceChange={(value, firstDue) => { setRecurrence(value); if (value && firstDue) setDueDate(firstDue) }} onInsertLink={() => setLinkOpen(true)}/>
          </div>

          {(quickSuggestionsLoading || quickSuggestions.length > 0) && <div className={styles.quickSuggestions}>
            <span className={styles.quickSuggestionsLabel}><AiBurstGlyph size={14}/>{t(quickSuggestionsLoading && !quickSuggestions.length ? 'Analyzing…' : 'Quick suggestions')}</span>
            <div>
              {quickSuggestions.length > 1 && <button className={styles.applyAllSuggestions} type="button" onClick={() => quickSuggestions.forEach(applyQuickSuggestion)}>{t('Apply all')}</button>}
              {quickSuggestions.map(suggestion => {
                if (suggestion.type === 'assignee') {
                  const user = data.users.find(item => item.id === suggestion.id)
                  return user ? <button key={`${suggestion.type}-${suggestion.id}`} type="button" onClick={() => applyQuickSuggestion(suggestion)}><Avatar name={user.displayName}/><span data-i18n-ignore>{t('Assign {name}').replace('{name}', user.displayName)}</span></button> : null
                }
                if (suggestion.type === 'project') {
                  const item = data.projects.find(project => project.id === suggestion.id)
                  return item ? <button key={`${suggestion.type}-${suggestion.id}`} type="button" onClick={() => applyQuickSuggestion(suggestion)}><ProjectGlyph project={item}/><span data-i18n-ignore>{t('Add to {name}').replace('{name}', item.name)}</span></button> : null
                }
                if (suggestion.type === 'label') {
                  const item = availableLabels.find(label => label.id === suggestion.id)
                  return item ? <button key={`${suggestion.type}-${suggestion.id}`} type="button" onClick={() => applyQuickSuggestion(suggestion)}><LabelIcon/><span data-i18n-ignore>{t('Add {name}').replace('{name}', item.name)}</span></button> : null
                }
                if (suggestion.type === 'team') {
                  const item = data.teams.find(team => team.id === suggestion.id)
                  return item ? <button key={`${suggestion.type}-${suggestion.id}`} type="button" onClick={() => applyQuickSuggestion(suggestion)}><TeamIcon team={item}/><span data-i18n-ignore>{t('Move to {name}').replace('{name}', item.name)}</span></button> : null
                }
                return null
              })}
            </div>
          </div>}

          {files.length > 0 && <div className={styles.attachments}>{files.map((file, index) => <span key={`${file.name}-${index}`}><Paperclip/><span>{file.name}</span><AttachmentRemoveButton label={`Remove ${file.name}`} onClick={() => setFiles(current => current.filter((_, item) => item !== index))}/></span>)}</div>}
          {error && <div className={styles.error} role="alert">{error}</div>}

          <footer className={styles.footer}>
            <button type="button" className={styles.attachButton} aria-label="Attach images, files, or videos" onClick={() => fileRef.current?.click()}><Paperclip/></button>
            <input ref={fileRef} type="file" hidden multiple onChange={addFiles}/>
            <label className={styles.createMore}><Toggle checked={createMore} label="Create more" onChange={setCreateMore}/><span>Create more</span></label>
            <button className={styles.submit} type="submit" disabled={saving}>{saving ? 'Creating…' : 'Create issue'}</button>
          </footer>
        </form>
      </Dialog.Content>
      <AddLinkDialog open={linkOpen} issueLabel="new issue" onOpenChange={setLinkOpen} onAdd={(url, label) => {
        const editor = descriptionEditorRef.current
        if (!editor) return
        editor.chain().focus().insertContent(label ? `<a href="${escapeAttribute(url)}">${escapeHtml(label)}</a>` : url).run()
      }}/>
      <DraftConfirmDialog open={confirmOpen} saving={saving} onCancel={() => setConfirmOpen(false)} onDiscard={() => void discardDraft()} onSave={() => void saveDraft()}/>
      <ExistingDraftDiscardDialog open={discardConfirmOpen} saving={saving} onCancel={() => setDiscardConfirmOpen(false)} onDiscard={() => void discardDraft()}/>
    </Dialog.Portal>
  </Dialog.Root>
}

/** "Discard this draft?" confirmation shared by the issue composers. */
export function ExistingDraftDiscardDialog({ description = 'Your draft will be deleted.', onCancel, onDiscard, open, primary = false, saving = false }: { description?: string; onCancel: () => void; onDiscard: () => void; open: boolean; primary?: boolean; saving?: boolean }) {
  const discardRef = useRef<HTMLButtonElement>(null)
  return <DraftDialogFrame description={description} descriptionId="existing-draft-discard-description" focusRef={discardRef} onCancel={onCancel} open={open} title="Discard this draft?"><div className={styles.confirmActions}><span/><span/><button type="button" disabled={saving} onClick={onCancel}>Cancel</button><button ref={discardRef} type="button" className={primary ? styles.confirmSave : styles.confirmDiscard} disabled={saving} onClick={onDiscard}>Discard</button></div></DraftDialogFrame>
}

function MiniProperty(props: React.ComponentProps<typeof PropertyMenu>) { return <PropertyMenu compact {...props}/> }
function EstimateGlyph({ value }: { value: number }) { return value ? <span aria-hidden="true" className="estimate-value-icon">{value}</span> : <CircleDashed aria-hidden="true"/> }
function cycleIssueProgress(issues: Issue[], cycleId: string) { const scoped=issues.filter(issue=>issue.cycleId===cycleId&&!issue.archivedAt);return scoped.length?Math.round(scoped.filter(issue=>issue.state.type==='completed'||issue.state.type==='canceled').length/scoped.length*100):0 }

/** The due date doubles as a recurring issue's first due date (the reference app's model). */
function MoreActions({ active, dueDate, recurrence, onDueDateChange, onRecurrenceChange, onInsertLink }: { active: boolean; dueDate: string; recurrence: string; onDueDateChange: (date: string) => void; onRecurrenceChange: (value: string, firstDue?: string) => void; onInsertLink: () => void }) {
  const { t, locale } = useI18n()
  const [open, setOpen] = useState(false)
  const [customOpen, setCustomOpen] = useState(false)
  // Without a due date a preset is first due on the date the API would pick
  // (one period from today, or the first matching day from tomorrow), and the
  // chip shows the due date that is actually sent (or that default).
  const firstDueFor = (value: string) => {
    if (dueDate) return dueDate
    const date = defaultRecurrenceFirstDue(value)
    return date ? toDateInput(date) : undefined
  }
  const presetAnchor = dueDate || toDateInput(recurrenceDate())
  const presets = useMemo(() => recurrencePresetOptions(recurrenceDate(presetAnchor), t, locale), [presetAnchor, locale, t])
  const summary = useRecurrenceSummary({ recurrence, dueDate: firstDueFor(recurrence) }, undefined, { firstDue: true })
  const [dateOpen, setDateOpen] = useState(false)
  useEffect(() => {
    if (!active) return
    const shortcut = (event: KeyboardEvent) => {
      // Shift+D inside the title or description is just a capital D.
      const typing = event.target instanceof HTMLElement && (event.target.isContentEditable || event.target.matches('input, textarea, select'))
      if (event.shiftKey && !event.metaKey && !event.ctrlKey && !event.altKey && !typing && event.key.toLowerCase() === 'd') {
        event.preventDefault()
        setOpen(true)
        setDateOpen(true)
      }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'l') {
        event.preventDefault()
        setOpen(false)
        onInsertLink()
      }
    }
    addEventListener('keydown', shortcut)
    return () => removeEventListener('keydown', shortcut)
  }, [active, onInsertLink])
  return <DropdownMenu.Root open={open} onOpenChange={setOpen}>
    <DropdownMenu.Trigger asChild><button type="button" className={styles.moreButton} aria-label="More actions"><MoreHorizontal/></button></DropdownMenu.Trigger>
    <DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" className={styles.moreMenu} align="start" sideOffset={4} collisionPadding={10}>
      <DropdownMenu.Sub open={dateOpen} onOpenChange={setDateOpen}>
        <DropdownMenu.SubTrigger className={styles.menuItem}><CalendarIcon/><span>Set due date</span><kbd>⇧ D</kbd><ChevronRight/></DropdownMenu.SubTrigger>
        <DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" className={styles.dateMenu} sideOffset={3} alignOffset={-5}><DueDateCommand value={dueDate} onSelect={async value => onDueDateChange(value)}/></DropdownMenu.SubContent></DropdownMenu.Portal>
      </DropdownMenu.Sub>
      <DropdownMenu.Sub><DropdownMenu.SubTrigger className={styles.menuItem}><Repeat2/><span>{recurrence ? t('Recurring') : t('Make recurring…')}</span><ChevronRight/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" className={styles.moreMenu} sideOffset={3}>{presets.map(option=><DropdownMenu.CheckboxItem className={styles.menuItem} checked={recurrence===option.value} key={option.value} onCheckedChange={()=>onRecurrenceChange(recurrence===option.value?'':option.value, recurrence===option.value?undefined:firstDueFor(option.value))}><Repeat2/><span>{option.label}</span>{recurrence===option.value&&<span>✓</span>}</DropdownMenu.CheckboxItem>)}<DropdownMenu.Item className={styles.menuItem} onSelect={()=>setCustomOpen(true)}><Repeat2/><span>{t('Custom…')}</span></DropdownMenu.Item>{recurrence&&<DropdownMenu.Item className={styles.menuItem} onSelect={()=>onRecurrenceChange('')}><X/><span>{t('Stop recurring')}</span></DropdownMenu.Item>}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
      <DropdownMenu.Item className={styles.menuItem} onSelect={onInsertLink}><Link2/><span>Add link…</span><kbd>Ctrl L</kbd></DropdownMenu.Item>
    </DropdownMenu.Content></DropdownMenu.Portal>
    {recurrence && <button type="button" className={styles.recurrenceChip} aria-label={t('Edit recurring schedule')} onClick={() => setCustomOpen(true)}><Repeat2/><span>{summary}</span></button>}
    <RecurrenceDialog open={customOpen} onOpenChange={setCustomOpen} value={recurrence || undefined} dueDate={dueDate || undefined} onSave={(value, nextFirstDue) => { onRecurrenceChange(value, nextFirstDue); setCustomOpen(false) }} onStop={() => { onRecurrenceChange(''); setCustomOpen(false) }}/>
  </DropdownMenu.Root>
}

function AddLinkDialog({ issueLabel, onAdd, onOpenChange, open }: { issueLabel: string; onAdd: (url: string, label: string) => void; onOpenChange: (open: boolean) => void; open: boolean }) {
  const { t } = useI18n()
  const title = t('Add link to {name}').replace('{name}', t(issueLabel))
  const [url, setUrl] = useState('')
  const [label, setLabel] = useState('')
  const submitLink = () => {
    const value = url.trim()
    if (!value) return
    onAdd(/^https?:\/\//i.test(value) ? value : `https://${value}`, label.trim())
    setUrl('')
    setLabel('')
    onOpenChange(false)
  }
  return <Dialog.Root open={open} onOpenChange={onOpenChange}>
    <Dialog.Portal>
      <Dialog.Overlay data-flow-motion="backdrop" className={styles.linkOverlay}/>
      <Dialog.Content data-flow-motion="dialog" className={styles.linkDialog} aria-label={title}>
        <Dialog.Title><ExternalLink/><span data-i18n-ignore>{title}</span></Dialog.Title>
        <label><span>URL</span><input autoFocus aria-label="URL" placeholder="https://…" value={url} onChange={event => setUrl(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); submitLink() } }}/></label>
        <label><span>Title <small>(optional)</small></span><input aria-label="Title (optional)" value={label} onChange={event => setLabel(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); submitLink() } }}/></label>
        <div><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button type="button" className={styles.addLink} disabled={!url.trim()} onClick={submitLink}>Add link</button></div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}

function DraftConfirmDialog({ onCancel, onDiscard, onSave, open, saving }: { onCancel: () => void; onDiscard: () => void; onSave: () => void; open: boolean; saving: boolean }) {
  const saveRef = useRef<HTMLButtonElement>(null)
  return <DraftDialogFrame description="You can finish this issue later from your drafts." descriptionId="draft-confirm-description" escapeLocked focusRef={saveRef} onCancel={onCancel} open={open} title="Save to drafts?"><div className={styles.confirmActions}><button type="button" disabled={saving} onClick={onDiscard}>Discard</button><span/><button type="button" disabled={saving} onClick={onCancel}>Cancel</button><button ref={saveRef} type="button" className={styles.confirmSave} disabled={saving} onClick={onSave}>{saving ? 'Saving...' : 'Save'}</button></div></DraftDialogFrame>
}

function DraftDialogFrame({ children, description, descriptionId, escapeLocked = false, focusRef, onCancel, open, title }: { children: ReactNode; description: string; descriptionId: string; escapeLocked?: boolean; focusRef: RefObject<HTMLButtonElement | null>; onCancel: () => void; open: boolean; title: string }) {
  return <Dialog.Root open={open} onOpenChange={next => { if (!next) onCancel() }}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className={styles.confirmOverlay}/><Dialog.Content data-flow-motion="dialog" className={styles.confirmDialog} aria-describedby={descriptionId} onOpenAutoFocus={event => { event.preventDefault(); focusRef.current?.focus() }} onEscapeKeyDown={escapeLocked ? event => { event.preventDefault(); onCancel() } : undefined} onPointerDownOutside={event => event.preventDefault()}><Dialog.Title>{title}</Dialog.Title><Dialog.Description id={descriptionId}>{description}</Dialog.Description>{children}</Dialog.Content></Dialog.Portal></Dialog.Root>
}

function readStoredDraft(key: string): StoredIssueDraft | null {
  try {
    const value = globalThis.localStorage?.getItem(key)
    return value ? JSON.parse(value) as StoredIssueDraft : null
  } catch {
    return null
  }
}

function writeStoredDraft(key: string, draft: StoredIssueDraft) {
  try { globalThis.localStorage?.setItem(key, JSON.stringify({ ...draft, updatedAt: new Date().toISOString() })) } catch { /* Draft persistence is best-effort in private browsing. */ }
}

function removeStoredDraft(key: string) {
  try { globalThis.localStorage?.removeItem(key) } catch { /* Draft persistence is best-effort in private browsing. */ }
}

function escapeHtml(value: string) { return value.replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]!) }
function escapeAttribute(value: string) { return escapeHtml(value) }
import { useCreateMotion } from '@/components/ui/motion';
import { TRIAGE_STATUS } from '@/components/triage/triage-model'
import { issueTemplatesForTeam, isWorkspaceIssueTemplate } from '@/lib/issue-templates'
import { TemplateGlyph } from '@/components/settings/template-glyphs'
import { TemplateFormFill } from './template-form-fields'
import { missingRequiredFields, templateFormDescription, type TemplateFormValues } from '@/lib/template-form'

/** Links a newly created issue to the issue it was created from ("Create related"). */
async function relateCreatedIssue(created: Issue, related: NonNullable<MyIssuesCreateContext['related']>) {
  if (related.kind === 'parent') await updateIssue(related.issueId, { parentId: created.id })
  else if (related.kind === 'blocked') await createRelation(related.issueId, 'blocks', created.id)
  else if (related.kind === 'blocking') await createRelation(related.issueId, 'blocked_by', created.id)
  else await createRelation(related.issueId, 'related', created.id)
}
