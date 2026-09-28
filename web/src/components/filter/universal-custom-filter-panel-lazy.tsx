/**
 * LS-0618 UniversalCustomFilterPanelShouldBeLazyLoaded — lazy entry + EmptyAdd* catalog.
 */
import { lazy, Suspense, type ComponentType } from 'react'
import type { FilterEntityType } from './filter-block-types'
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
function entityPanel(entityType: FilterEntityType): ComponentType<Omit<UniversalCustomFilterPanelProps, 'entityType'>> {
  return function EntityUniversalCustomFilterPanel(props) {
    return <UniversalCustomFilterPanelShouldBeLazyLoaded {...props} entityType={entityType} />
  }
}

export const IssueUniversalCustomFilterPanelShouldBeLazyLoaded = entityPanel('issue')
export const ProjectUniversalCustomFilterPanelShouldBeLazyLoaded = entityPanel('project')
export const InitiativeUniversalCustomFilterPanelShouldBeLazyLoaded = entityPanel('initiative')
export const TeamUniversalCustomFilterPanelShouldBeLazyLoaded = entityPanel('team')
export const PullRequestUniversalCustomFilterPanelShouldBeLazyLoaded = entityPanel('pullRequest')
export const NotificationUniversalCustomFilterPanelShouldBeLazyLoaded = entityPanel('notification')
export const CustomerUniversalCustomFilterPanelShouldBeLazyLoaded = entityPanel('customer')
export const DocumentUniversalCustomFilterPanelShouldBeLazyLoaded = entityPanel('document')
export const MemberUniversalCustomFilterPanelShouldBeLazyLoaded = entityPanel('member')
export const FeedItemUniversalCustomFilterPanelShouldBeLazyLoaded = entityPanel('feedItem')
export const SearchResultUniversalCustomFilterPanelShouldBeLazyLoaded = entityPanel('searchResult')
export const WorkflowDefinitionUniversalCustomFilterPanelShouldBeLazyLoaded = entityPanel('workflowDefinition')

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
