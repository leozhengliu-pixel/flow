import { Fragment, useEffect, useMemo, useState } from 'react'
import { CustomerPageEmptyIcon } from '@/components/customer-detail/customer-page-icons'
import { AppLink } from '@/components/ui/app-link'
import { FlowTooltip } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import { projectPath } from '@/lib/app-routes'
import type { BootstrapData, Customer, CustomerRequest, Issue, Project } from '@/types/flow'
import { CustomerLogoPile } from './customer-logo-pile'
import { CustomerRequestDisplayMenu } from './customer-request-display-menu'
import { ImportantIcon, RequestPlusIcon } from './customer-request-glyphs'
import { applyRequestOverlay, customersSummary, EMPTY_OVERLAY, groupIsImportant, groupRequestsByCustomer, orderRequestGroups, readCustomerRequestView, writeCustomerRequestView, type CustomerRequestViewPreferences, type RequestOverlay } from './customer-request-model'
import { CustomerRequestPickerHost } from './customer-request-picker'
import { ADD_CUSTOMER_REQUEST_SHORTCUT, ADD_PROJECT_CUSTOMER_REQUEST_EVENT, isAddCustomerRequestShortcut, openCustomerRequestPicker, pendingProjectPick, projectCustomerRequests, requestProjectCustomerRequest, setPendingProjectPick, subscribePendingProjectPick, type CustomerPick } from './customer-request-events'
import { EmbeddedCustomerNeedForm } from './embedded-customer-need-form'
import { EmbeddedCustomerNeedRow } from './embedded-customer-need-row'
import './project-customer-requests.css'

const DOCS_URL = 'https://linear.app/docs/customer-requests'

/**
 * Starts "Add customer request…" on a project page (⌘K, ⌃R, the overview's Customers row):
 * "Select customer…", then the Customers tab with the composer open.
 */
export function ProjectCustomerRequestsLauncher({ data, project, onOpenRequests }: { data: BootstrapData; project: Project; onOpenRequests: () => void }) {
  useEffect(() => {
    const start = () => openCustomerRequestPicker(pick => { setPendingProjectPick({ projectId: project.id, pick, key: Date.now() }); onOpenRequests() })
    const onAdd = (event: Event) => {
      if ((event as CustomEvent<{ projectId: string }>).detail?.projectId !== project.id) return
      event.preventDefault()
      start()
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || !isAddCustomerRequestShortcut(event) || document.querySelector('[role=dialog][data-state=open], [role=menu]')) return
      event.preventDefault()
      start()
    }
    window.addEventListener(ADD_PROJECT_CUSTOMER_REQUEST_EVENT, onAdd)
    window.addEventListener('keydown', onKeyDown)
    return () => {
      window.removeEventListener(ADD_PROJECT_CUSTOMER_REQUEST_EVENT, onAdd)
      window.removeEventListener('keydown', onKeyDown)
    }
  }, [project.id, onOpenRequests])
  return <CustomerRequestPickerHost customers={data.customers}/>
}

/**
 * The overview's "Customers" row (Linear's ProjectOverviewPage `ir`): "Add customer request" while
 * the project has none, else the customers' logos and names linking to the Customers tab, and "+".
 */
export function ProjectCustomersRow({ data, project, issueIds }: { data: BootstrapData; project: Project; issueIds: ReadonlySet<string> }) {
  const { t } = useI18n()
  const requests = useMemo(() => projectCustomerRequests(data.customerRequests ?? [], project, issueIds), [data.customerRequests, project, issueIds])
  const customers = useMemo(() => {
    const byId = new Map((data.customers ?? []).map(customer => [customer.id, customer]))
    const counts = new Map<string, { customer: Customer; requests: number; important: boolean }>()
    for (const request of requests) {
      const customer = request.customerId ? byId.get(request.customerId) : undefined
      if (!customer) continue
      const entry = counts.get(customer.id) ?? { customer, requests: 0, important: false }
      entry.requests += 1
      entry.important ||= (request.priority ?? 0) >= 1
      counts.set(customer.id, entry)
    }
    // Linear's `sortedCustomerByImpact`: important customers, then by revenue and request count.
    return [...counts.values()].sort((left, right) => Number(right.important) - Number(left.important) || (right.customer.annualRevenue ?? 0) - (left.customer.annualRevenue ?? 0) || right.requests - left.requests).map(entry => entry.customer)
  }, [requests, data.customers])
  const hasUnknown = requests.some(request => !request.customerId || !(data.customers ?? []).some(customer => customer.id === request.customerId))
  const add = () => requestProjectCustomerRequest(project.id)
  const summary = customersSummary(customers.map(customer => customer.name))
  return <section className="project-overview__row-section project-customers-row">
    <h3>{t('Customers')}</h3>
    <div className="project-overview__row-content">
      {requests.length === 0
        ? <button className="project-customers-row__add" type="button" onClick={add}><RequestPlusIcon/><span>{t('Add customer request')}</span></button>
        : <>
            <FlowTooltip label={t('Open project customer requests')}>
              <AppLink className="project-customers-row__summary" href={projectPath(data.workspace.urlKey, project, 'requests')}>
                <CustomerLogoPile customers={customers} appendNoCustomer={hasUnknown && customers.length <= 3} size={16}/>
                <span className="project-customers-row__names" data-i18n-ignore>
                  {summary.names.map((name, index) => <Fragment key={`${name}-${index}`}>{name}{index < summary.names.length - 1 ? (index === summary.names.length - 2 && !summary.rest ? ` ${t('and')} ` : ', ') : ''}</Fragment>)}
                  {summary.rest > 0 && <>{` ${t('and')} `}{summary.rest} {t(summary.rest === 1 ? 'other' : 'others')}</>}
                  {!customers.length && t('Unknown customer')}
                </span>
              </AppLink>
            </FlowTooltip>
            <FlowTooltip label={t('Add customer request')} shortcut={ADD_CUSTOMER_REQUEST_SHORTCUT()}>
              <button className="project-customers-row__add is-icon" type="button" aria-label={t('Add customer need')} onClick={add}><RequestPlusIcon/></button>
            </FlowTooltip>
          </>}
    </div>
  </section>
}

/**
 * The project's Customers tab (Linear's ProjectCustomerNeedsPage): "Customers" with the request
 * and important counts, "Add request", the composer, one row per customer, or the empty state.
 */
export function ProjectCustomerRequestsPage({ data, project, issues }: { data: BootstrapData; project: Project; issues: Issue[] }) {
  const { t } = useI18n()
  const issueIds = useMemo(() => new Set(issues.map(issue => issue.id)), [issues])
  const issueById = useMemo(() => new Map(issues.map(issue => [issue.id, issue])), [issues])
  const [overlay, setOverlay] = useState<RequestOverlay>(EMPTY_OVERLAY)
  const [composer, setComposer] = useState<(CustomerPick & { key: number }) | null>(() => { const pending = pendingProjectPick(); return pending?.projectId === project.id ? { ...pending.pick, key: pending.key } : null })
  const [view, setViewState] = useState<CustomerRequestViewPreferences>(() => readCustomerRequestView('project'))
  const setView = (next: CustomerRequestViewPreferences) => { setViewState(next); writeCustomerRequestView('project', next) }

  useEffect(() => {
    const take = () => {
      const pending = pendingProjectPick()
      if (pending?.projectId !== project.id) return
      setComposer({ ...pending.pick, key: pending.key })
      setPendingProjectPick(undefined)
    }
    take()
    return subscribePendingProjectPick(take)
  }, [project.id])

  const customers = useMemo(() => new Map((data.customers ?? []).map(customer => [customer.id, customer])), [data.customers])
  const visible = useMemo(() => applyRequestOverlay(data.customerRequests ?? [], overlay, request => !request.archivedAt && (request.projectId === project.id || Boolean(request.issueId && issueIds.has(request.issueId)))), [data.customerRequests, overlay, project.id, issueIds])
  const groups = useMemo(() => orderRequestGroups(groupRequestsByCustomer(visible, customers), view), [visible, customers, view])
  const importantCount = groups.filter(groupIsImportant).length
  const changed = (request: CustomerRequest) => setOverlay(current => ({ upserts: new Map(current.upserts).set(request.id, request), removed: current.removed }))
  const deleted = (id: string) => setOverlay(current => ({ upserts: current.upserts, removed: new Set(current.removed).add(id) }))
  const add = () => {
    if (composer) { document.querySelector<HTMLTextAreaElement>('.project-customer-requests .embedded-customer-need-form textarea')?.focus(); return }
    openCustomerRequestPicker(pick => setComposer({ ...pick, key: Date.now() }))
  }

  return <div className="project-customer-requests">
    <div className="project-customer-requests__header">
      <div className="project-customer-requests__title">
        <span>{t('Customers')}</span>
        <span className="project-customer-requests__count">{groups.length}</span>
        {importantCount > 0 && <FlowTooltip label={t('Important to {count} customers').replace('{count}', String(importantCount))}>
          <span className="project-customer-requests__important"><ImportantIcon size={16}/>{importantCount}</span>
        </FlowTooltip>}
      </div>
      <div className="project-customer-requests__actions">
        {groups.length > 0 && <CustomerRequestDisplayMenu view={view} onChange={setView} className="project-customer-requests__icon-button"/>}
        <FlowTooltip label={t('Add customer request to project')} shortcut={ADD_CUSTOMER_REQUEST_SHORTCUT()}>
          <button className="project-customer-requests__add" type="button" aria-label={t('Add customer request')} onClick={add}><RequestPlusIcon size={14}/><span>{t('Add request')}</span></button>
        </FlowTooltip>
      </div>
    </div>
    {composer && <EmbeddedCustomerNeedForm
      key={composer.key}
      data={data}
      host="projectPage"
      projectId={project.id}
      initialCustomerId={composer.customerId}
      pendingCustomerName={composer.pendingCustomerName}
      initialSourceUrl={composer.sourceUrl}
      className="project-customer-requests__composer"
      onCancel={() => setComposer(null)}
      onCreated={request => { changed(request); setComposer(null) }}
    />}
    {groups.length === 0
      ? <div className="project-customer-requests__empty">
          <CustomerPageEmptyIcon className="project-customer-requests__empty-icon" height={80}/>
          <div className="project-customer-requests__empty-copy">
            <span className="project-customer-requests__empty-title">{t('Customer requests')}</span>
            <span className="project-customer-requests__empty-text">{t('No customer requests created yet. Use a supported integration to automatically create requests, or create one manually.')}</span>
          </div>
          <div className="project-customer-requests__empty-actions">
            <button className="project-customer-requests__primary" type="button" disabled={Boolean(composer)} aria-label={t('Add customer request to project')} onClick={add}>
              {t('Add request')}<span className="project-customer-requests__kbd" aria-hidden="true">{ADD_CUSTOMER_REQUEST_SHORTCUT().split(' ').map(key => <kbd key={key}>{key}</kbd>)}</span>
            </button>
            <a className="project-customer-requests__secondary" href={DOCS_URL} target="_blank" rel="noopener noreferrer">{t('Documentation')}</a>
          </div>
        </div>
      : <div className="project-customer-requests__list">
          {groups.map(group => <div className="project-customer-requests__row" key={group.key}>
            <EmbeddedCustomerNeedRow data={data} group={group} variant="projectPage" issueFor={request => request.issueId ? issueById.get(request.issueId) ?? data.issues.find(issue => issue.id === request.issueId) : undefined} onChanged={changed} onDeleted={deleted}/>
          </div>)}
        </div>}
  </div>
}
