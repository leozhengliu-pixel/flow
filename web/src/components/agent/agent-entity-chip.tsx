import { useContext } from 'react'
import { NodeViewWrapper, type ReactNodeViewProps } from '@tiptap/react'
import { ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import type { BootstrapData } from '@/types/flow'
import { agentEntityById, type AgentEntityRef } from './agent-answer-content'
import { AgentEntityDataContext, agentEntityHref } from './agent-entity-data'
import styles from './agent-answer.module.css'

/** Linear's inline "⁠FLO-3 Import your data" chip: status (or project) icon, secondary identifier, primary title. */
export function AgentEntityChip({ data, entity }: { data: BootstrapData; entity: AgentEntityRef }) {
  return (
    <a className={styles.entityChip} contentEditable={false} data-agent-entity={entity.kind} data-i18n-ignore href={agentEntityHref(data, entity)}>
      {entity.kind === 'issue' ? (
        <>
          <StatusIcon size={14} state={entity.issue.state} />
          <span className={styles.entityIdentifier}>{entity.issue.identifier}</span>
          <span className={styles.entityTitle}>{entity.issue.title}</span>
        </>
      ) : (
        <>
          <ProjectIcon size={14} style={{ color: entity.project.color }} />
          <span className={styles.entityTitle}>{entity.project.name}</span>
        </>
      )}
    </a>
  )
}

/** Tiptap node view for AgentEntityNode; unknown entities fall back to their plain label. */
export function AgentEntityChipView({ node }: ReactNodeViewProps) {
  const data = useContext(AgentEntityDataContext)
  const entity = data ? agentEntityById(data, String(node.attrs.kind), String(node.attrs.id)) : undefined
  return (
    <NodeViewWrapper as="span" className={styles.entityWrap}>
      {data && entity ? <AgentEntityChip data={data} entity={entity} /> : String(node.attrs.label ?? '')}
    </NodeViewWrapper>
  )
}
