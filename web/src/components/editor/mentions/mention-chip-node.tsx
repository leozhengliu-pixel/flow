import { useContext, useMemo } from 'react'
import { NodeViewWrapper, type ReactNodeViewProps } from '@tiptap/react'
import { AgentEntityChip } from '@/components/agent/agent-entity-chip'
import { AgentEntityDataContext, useAgentEntityData } from '@/components/agent/agent-entity-data'
import { useAgentEntity } from '@/components/agent/agent-entity-fetch'
import { AppLink } from '@/components/ui/app-link'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData } from '@/types/flow'
import { mentionKind, mentionTarget, type MentionAttrs } from './mention-model'
import styles from '@/components/agent/agent-answer.module.css'

function fallbackText(attrs: MentionAttrs, user: boolean) {
  const label = String(attrs.label ?? '')
  return user ? `@${label}` : [label, attrs.mentionType === 'issue' ? attrs.title : ''].filter(Boolean).join(' ')
}

/** The label while the resource loads (or the workspace data is not at hand): the stored text, no link. */
function MentionPending({ attrs }: { attrs: MentionAttrs }) {
  const text = fallbackText(attrs, (attrs.mentionType ?? 'user') === 'user')
  return <span className={styles.entityPending} data-flow-mention={attrs.id} data-i18n-ignore data-mention-state="loading">{text}</span>
}

/** The stored label, marked unavailable, when the resource is gone or the viewer cannot open it. */
function MentionMissing({ attrs }: { attrs: MentionAttrs }) {
  const { t } = useI18n()
  const user = (attrs.mentionType ?? 'user') === 'user'
  const text = fallbackText(attrs, user)
  const className = `${styles.entityMissing}${user ? '' : ` ${styles.entityChip}`}`
  const common = { className, 'data-agent-entity': attrs.mentionType, 'data-flow-mention': attrs.id, 'data-i18n-ignore': true, 'data-mention-state': 'missing', title: t('This item was deleted or you do not have access to it') }
  return attrs.href && !user
    ? <AppLink {...common} contentEditable={false} href={attrs.href}>{text}</AppLink>
    : <span {...common}>{text}</span>
}

function ResolvedMention({ attrs, data, target }: { attrs: MentionAttrs; data: BootstrapData; target: NonNullable<ReturnType<typeof mentionTarget>> }) {
  const state = useAgentEntity(data, target)
  if (state.status === 'ready') return <AgentEntityChip data={data} entity={state.entity}/>
  return state.status === 'loading' ? <MentionPending attrs={attrs}/> : <MentionMissing attrs={attrs}/>
}

/**
 * The reference chip in editable and read-only rich text: the same chip, hover card, in-app navigation (cmd/ctrl or
 * middle-click opens a new tab) and by-id fetch (paged workspaces) as an agent answer's mentions.
 */
export function MentionChipView({ node }: ReactNodeViewProps) {
  const contextData = useContext(AgentEntityDataContext)
  const data = useAgentEntityData(contextData)
  const attrs = node.attrs as MentionAttrs
  const target = useMemo(() => mentionTarget(attrs, data), [attrs, data])
  const kind = mentionKind(attrs, data)
  return (
    <NodeViewWrapper as="span" className={styles.entityWrap} data-mention-kind={kind}>
      {data && target
        ? <ResolvedMention attrs={attrs} data={data} target={target}/>
        : data ? <MentionMissing attrs={attrs}/> : <MentionPending attrs={attrs}/>}
    </NodeViewWrapper>
  )
}
