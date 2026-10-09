import { useContext, useMemo } from 'react'
import { AgentEntityChip } from '@/components/agent/agent-entity-chip'
import { AgentEntityDataContext, useAgentEntityData } from '@/components/agent/agent-entity-data'
import { useAgentEntity } from '@/components/agent/agent-entity-fetch'
import { AppLink } from '@/components/ui/app-link'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData } from '@/types/flow'
import { mentionTarget, type MentionAttrs } from './mention-model'
import styles from '@/components/agent/agent-answer.module.css'

/** A resource named outside rich text (activity history, event lines): the kind, a resolvable key and the label to fall back to. */
export type MentionReferenceProps = { type: string; id: string; label: string; title?: string; href?: string }

function attrsOf({ type, id, label, title, href }: MentionReferenceProps): MentionAttrs {
  return { mentionType: type, id, label, title: title ?? '', href: href ?? '' }
}

function ReferenceFallback({ attrs, status }: { attrs: MentionAttrs; status: 'loading' | 'missing' }) {
  const { t } = useI18n()
  const user = attrs.mentionType === 'user'
  const text = user ? `@${attrs.label}` : [attrs.label, attrs.mentionType === 'issue' ? attrs.title : ''].filter(Boolean).join(' ')
  if (status === 'loading') return <span className={styles.entityPending} data-i18n-ignore data-mention-state="loading">{text}</span>
  const common = { className: `${styles.entityMissing}${user ? '' : ` ${styles.entityChip}`}`, 'data-agent-entity': attrs.mentionType, 'data-i18n-ignore': true, 'data-mention-state': 'missing', title: t('This item was deleted or you do not have access to it') }
  return attrs.href && !user ? <AppLink {...common} href={attrs.href}>{text}</AppLink> : <span {...common}>{text}</span>
}

function ResolvedReference({ attrs, data, target }: { attrs: MentionAttrs; data: BootstrapData; target: NonNullable<ReturnType<typeof mentionTarget>> }) {
  const state = useAgentEntity(data, target)
  if (state.status === 'ready') return <AgentEntityChip data={data} entity={state.entity}/>
  return <ReferenceFallback attrs={attrs} status={state.status}/>
}

/**
 * The inline reference chip for a resource named in activity history or an event line: the same chip, hover card, in-app
 * navigation and by-id fetch (issues and projects a paged workspace does not hold) as a mention in rich text. A resource that
 * is gone keeps its stored label, marked unavailable.
 */
export function MentionReference(props: MentionReferenceProps) {
  const contextData = useContext(AgentEntityDataContext)
  const data = useAgentEntityData(contextData)
  const attrs = attrsOf(props)
  const target = useMemo(() => mentionTarget(attrsOf(props), data), [props.type, props.id, props.label, props.title, props.href, data]) // eslint-disable-line react-hooks/exhaustive-deps
  return <span className={styles.entityWrap} data-mention-kind={props.type}>
    {data && target ? <ResolvedReference attrs={attrs} data={data} target={target}/> : <ReferenceFallback attrs={attrs} status={data ? 'missing' : 'loading'}/>}
  </span>
}
