import { useContext } from 'react'
import { NodeViewWrapper, type ReactNodeViewProps } from '@tiptap/react'
import { AppLink } from '@/components/ui/app-link'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData } from '@/types/flow'
import { AgentEntityDataContext } from './agent-entity-data'
import { useAgentEntity } from './agent-entity-fetch'
import { AgentEntityHover } from './agent-entity-hover'
import { chipParts } from './agent-entity-chip-parts'
import { agentEntityPath, isAgentTextMention, type AgentEntity, type AgentEntityKind } from './agent-entity-refs'
import styles from './agent-answer.module.css'

/**
 * Linear's inline mention: a tinted chip with the resource's own icon, a secondary identifier and a primary title.
 * Hovering or focusing it opens the resource's card; clicking navigates (cmd/ctrl/middle-click opens a new tab).
 */
export function AgentEntityChip({ data, entity }: { data: BootstrapData; entity: AgentEntity }) {
  const { t } = useI18n()
  const parts = chipParts(entity, t)
  const textMention = entity.kind !== 'link' && isAgentTextMention(entity.kind)
  return (
    <AgentEntityHover data={data} entity={entity}>
      <AppLink className={textMention ? `${styles.entityChip} ${styles.entityMention}` : styles.entityChip} contentEditable={false} data-agent-entity={entity.kind === 'link' ? entity.icon : entity.kind} data-i18n-ignore href={agentEntityPath(data, entity)}>
        {parts.icon && <span className={styles.entityLead}>{parts.icon}{' '}</span>}
        {parts.identifier && <><span className={styles.entityIdentifier}>{parts.identifier}</span>{' '}</>}
        <span className={styles.entityTitle}>{parts.label}</span>
        {parts.suffix && <span className={styles.entityIdentifier}>{' · '}{parts.suffix}</span>}
      </AppLink>
    </AgentEntityHover>
  )
}

/** Tiptap node view for AgentEntityNode; entities that cannot be found fall back to their plain label. */
export function AgentEntityChipView({ node }: ReactNodeViewProps) {
  const data = useContext(AgentEntityDataContext)
  const kind = String(node.attrs.kind) as AgentEntityKind
  const label = String(node.attrs.label ?? '')
  const state = useAgentEntity(data, { kind, id: String(node.attrs.id), label, href: node.attrs.href ? String(node.attrs.href) : undefined })
  return (
    <NodeViewWrapper as="span" className={styles.entityWrap}>
      {data && state.status === 'ready' ? <AgentEntityChip data={data} entity={state.entity}/> : <span className={state.status === 'loading' ? styles.entityPending : undefined}>{label}</span>}
    </NodeViewWrapper>
  )
}
