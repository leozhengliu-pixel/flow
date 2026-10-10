/**
 * LS-0618 UniversalCustomFilterPanelShouldBeLazyLoaded — lazy entry + EmptyAdd* catalog.
 */
import { lazy, Suspense } from 'react'
import type { UniversalCustomFilterPanelProps } from './universal-custom-filter-panel'
import { RegisterFilterValuesShouldBeLazyLoaded } from './register-filter-values-lazy'
import styles from './universal-custom-filter-panel.module.css'

const PanelLazy = lazy(async () => {
  const mod = await import('./universal-custom-filter-panel')
  return { default: mod.UniversalCustomFilterPanel }
})

export type UniversalCustomFilterPanelShouldBeLazyLoadedProps = UniversalCustomFilterPanelProps & {
  /** When true, also mount the registry lazy companion for this entity. Default true. */
  registerPack?: boolean
}

export function UniversalCustomFilterPanelShouldBeLazyLoaded({
  registerPack = true,
  ...props
}: UniversalCustomFilterPanelShouldBeLazyLoadedProps) {
  return (
    <>
      {registerPack && <RegisterFilterValuesShouldBeLazyLoaded type={props.entityType} />}
      <Suspense fallback={<div className={styles.panel} data-state="loading"><div className={styles.loading}>Loading filters…</div></div>}>
        <PanelLazy {...props} />
      </Suspense>
    </>
  )
}

/** Per-entity lazy panel aliases (Linear *UniversalCustomFilterPanelShouldBeLazyLoaded). */
type EntityPanelProps = Omit<UniversalCustomFilterPanelProps, 'entityType'>

export function IssueUniversalCustomFilterPanelShouldBeLazyLoaded(props: EntityPanelProps) {
  return <UniversalCustomFilterPanelShouldBeLazyLoaded {...props} entityType="issue" />
}
export function ProjectUniversalCustomFilterPanelShouldBeLazyLoaded(props: EntityPanelProps) {
  return <UniversalCustomFilterPanelShouldBeLazyLoaded {...props} entityType="project" />
}
export function InitiativeUniversalCustomFilterPanelShouldBeLazyLoaded(props: EntityPanelProps) {
  return <UniversalCustomFilterPanelShouldBeLazyLoaded {...props} entityType="initiative" />
}
export function TeamUniversalCustomFilterPanelShouldBeLazyLoaded(props: EntityPanelProps) {
  return <UniversalCustomFilterPanelShouldBeLazyLoaded {...props} entityType="team" />
}
export function PullRequestUniversalCustomFilterPanelShouldBeLazyLoaded(props: EntityPanelProps) {
  return <UniversalCustomFilterPanelShouldBeLazyLoaded {...props} entityType="pullRequest" />
}
export function NotificationUniversalCustomFilterPanelShouldBeLazyLoaded(props: EntityPanelProps) {
  return <UniversalCustomFilterPanelShouldBeLazyLoaded {...props} entityType="notification" />
}
export function CustomerUniversalCustomFilterPanelShouldBeLazyLoaded(props: EntityPanelProps) {
  return <UniversalCustomFilterPanelShouldBeLazyLoaded {...props} entityType="customer" />
}
export function DocumentUniversalCustomFilterPanelShouldBeLazyLoaded(props: EntityPanelProps) {
  return <UniversalCustomFilterPanelShouldBeLazyLoaded {...props} entityType="document" />
}
export function MemberUniversalCustomFilterPanelShouldBeLazyLoaded(props: EntityPanelProps) {
  return <UniversalCustomFilterPanelShouldBeLazyLoaded {...props} entityType="member" />
}
export function FeedItemUniversalCustomFilterPanelShouldBeLazyLoaded(props: EntityPanelProps) {
  return <UniversalCustomFilterPanelShouldBeLazyLoaded {...props} entityType="feedItem" />
}
export function SearchResultUniversalCustomFilterPanelShouldBeLazyLoaded(props: EntityPanelProps) {
  return <UniversalCustomFilterPanelShouldBeLazyLoaded {...props} entityType="searchResult" />
}
export function WorkflowDefinitionUniversalCustomFilterPanelShouldBeLazyLoaded(props: EntityPanelProps) {
  return <UniversalCustomFilterPanelShouldBeLazyLoaded {...props} entityType="workflowDefinition" />
}

/** EmptyAdd* lazy buttons — thin wrappers that open the panel empty state. */
export {
  EmptyAddFilterButton as EmptyAddIssueFilterButtonShouldBeLazyLoaded,
  EmptyAddFilterButton as EmptyAddProjectFilterButtonShouldBeLazyLoaded,
  EmptyAddFilterButton as EmptyAddInitiativeFilterButtonShouldBeLazyLoaded,
  EmptyAddFilterButton as EmptyAddTeamFilterButtonShouldBeLazyLoaded,
  EmptyAddFilterButton as EmptyAddPullRequestFilterButtonShouldBeLazyLoaded,
  EmptyAddFilterButton as EmptyAddNotificationFilterButtonShouldBeLazyLoaded,
  EmptyAddFilterButton as EmptyAddCustomerFilterButtonShouldBeLazyLoaded,
  EmptyAddFilterButton as EmptyAddDocumentFilterButtonShouldBeLazyLoaded,
  EmptyAddFilterButton as EmptyAddMemberFilterButtonShouldBeLazyLoaded,
  EmptyAddFilterButton as EmptyAddFeedItemFilterButtonShouldBeLazyLoaded,
} from './universal-custom-filter-panel'
