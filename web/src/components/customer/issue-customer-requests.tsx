import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { DisclosureTriangle } from '@/components/ui/disclosure-triangle'
import { FlowTooltip } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Customer, CustomerRequest } from '@/types/flow'
import { CustomerRequestDisplayMenu } from './customer-request-display-menu'
import { RequestPlusIcon } from './customer-request-glyphs'
import { applyRequestOverlay, EMPTY_OVERLAY, groupRequestsByCustomer, orderRequestGroups, readCustomerRequestView, writeCustomerRequestView, type CustomerRequestViewPreferences, type RequestOverlay } from './customer-request-model'
import { CustomerRequestPickerHost } from './customer-request-picker'
import { ADD_CUSTOMER_REQUEST_EVENT, ADD_CUSTOMER_REQUEST_SHORTCUT, isAddCustomerRequestShortcut, openCustomerRequestPicker, type CustomerPick } from './customer-request-events'
import { EmbeddedCustomerNeedForm } from './embedded-customer-need-form'
import { CUSTOMER_REQUEST_HASH, EmbeddedCustomerNeedRow } from './embedded-customer-need-row'
import './issue-customer-requests.css'

const OPEN_KEY = 'flow:issue-customer-requests-open'

function readOpen() {
  try { return localStorage.getItem(OPEN_KEY) !== 'closed' } catch { return true }
}

/**
 * The issue page's "Customers" section (Linear's IssueDetailsPaneSidebar `gu`): a sticky header
 * with the section toggle, display options and "+", the composer, and one row per customer.
 * Hidden until the issue has a request or one is being added.
 */
export function IssueCustomerRequests({ data, issueId, requests, onLocalCreated }: {
  data: BootstrapData
  issueId: string
  requests: CustomerRequest[]
  onLocalCreated?: (request: CustomerRequest) => void
}) {
  const { t } = useI18n()
  const [open, setOpenState] = useState(readOpen)
  const [composer, setComposer] = useState<(CustomerPick & { key: number }) | null>(null)
  const [overlay, setOverlay] = useState<RequestOverlay>(EMPTY_OVERLAY)
  const [localCustomers, setLocalCustomers] = useState<Customer[]>([])
  const [view, setViewState] = useState<CustomerRequestViewPreferences>(() => readCustomerRequestView('issue'))
  const sectionRef = useRef<HTMLElement>(null)
  const addRef = useRef<HTMLButtonElement>(null)
  const hash = typeof window === 'undefined' ? '' : decodeURIComponent(window.location.hash.replace(/^#/, ''))

  const setOpen = (next: boolean) => {
    setOpenState(next)
    try { localStorage.setItem(OPEN_KEY, next ? 'open' : 'closed') } catch { /* storage unavailable */ }
  }
  const setView = (next: CustomerRequestViewPreferences) => { setViewState(next); writeCustomerRequestView('issue', next) }

  const workspace = useMemo(() => localCustomers.length ? { ...data, customers: [...data.customers, ...localCustomers.filter(customer => !data.customers.some(item => item.id === customer.id))] } : data, [data, localCustomers])
  const customers = useMemo(() => new Map(workspace.customers.map(customer => [customer.id, customer])), [workspace.customers])
  const visible = useMemo(() => applyRequestOverlay(requests, overlay, request => request.issueId === issueId && !request.archivedAt), [requests, overlay, issueId])
  const groups = useMemo(() => orderRequestGroups(groupRequestsByCustomer(visible, customers), view), [visible, customers, view])

  const startRequest = useCallback(() => {
    if (composer) {
      sectionRef.current?.querySelector<HTMLTextAreaElement>('.embedded-customer-need-form textarea')?.focus()
      return
    }
    openCustomerRequestPicker(pick => { setOpen(true); setComposer({ ...pick, key: Date.now() }) })
  }, [composer])

  // "Add customer request…" from the issue menu or ⌘K.
  useEffect(() => {
    const onAdd = (event: Event) => {
      if ((event as CustomEvent<{ issueId: string }>).detail?.issueId !== issueId) return
      event.preventDefault()
      startRequest()
    }
    window.addEventListener(ADD_CUSTOMER_REQUEST_EVENT, onAdd)
    return () => window.removeEventListener(ADD_CUSTOMER_REQUEST_EVENT, onAdd)
  }, [issueId, startRequest])

  // ⌃R adds a request; ⌃⇧F opens or closes the section (Linear's "Open/Close requests section").
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || document.querySelector('[role=dialog][data-state=open], [role=menu]')) return
      if (isAddCustomerRequestShortcut(event)) {
        event.preventDefault()
        startRequest()
      } else if (event.ctrlKey && event.shiftKey && !event.metaKey && event.key.toLowerCase() === 'f' && (visible.length || composer)) {
        event.preventDefault()
        setOpen(!open)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [startRequest, open, visible.length, composer]) // eslint-disable-line react-hooks/exhaustive-deps

  // A `#customerRequest-{id}` link opens the section and reveals the request.
  useEffect(() => {
    if (!hash.startsWith(CUSTOMER_REQUEST_HASH)) return
    const id = hash.slice(CUSTOMER_REQUEST_HASH.length)
    if (!visible.some(request => request.id === id)) return
    setOpen(true)
    requestAnimationFrame(() => document.getElementById(hash)?.scrollIntoView({ block: 'center' }))
  }, [hash, visible.length]) // eslint-disable-line react-hooks/exhaustive-deps

  const changed = (request: CustomerRequest) => {
    setOverlay(current => ({ upserts: new Map(current.upserts).set(request.id, request), removed: current.removed }))
  }
  const deleted = (id: string) => {
    setOverlay(current => ({ upserts: current.upserts, removed: new Set(current.removed).add(id) }))
  }

  const picker = <CustomerRequestPickerHost customers={workspace.customers}/>
  if (!visible.length && !composer) return picker
  const pendingFor = (group: { primary: CustomerRequest; additional: CustomerRequest[] }) => [group.primary, ...group.additional].some(request => hash === `${CUSTOMER_REQUEST_HASH}${request.id}`)
  return <>
    {picker}
    <section ref={sectionRef} className="issue-customer-requests" aria-label={t('Customers')}>
      <div className={`issue-customer-requests__header${open ? ' is-sticky' : ''}`}>
        <div className="issue-customer-requests__title">
          <button className="issue-customer-requests__toggle" type="button" aria-label={t(open ? 'Collapse customers section' : 'Expand customers section')} aria-expanded={open} onClick={() => setOpen(!open)}>
            <DisclosureTriangle open={open} className="issue-customer-requests__caret"/>
            <span>{t('Customers')}</span>
          </button>
          {!open && <span className="issue-customer-requests__count">{new Set(visible.map(request => request.customerId || request.id)).size}</span>}
        </div>
        {open && <div className="issue-customer-requests__actions">
          <CustomerRequestDisplayMenu view={view} onChange={setView} className="issue-customer-requests__button"/>
          <FlowTooltip label={t('Add customer request')} shortcut={ADD_CUSTOMER_REQUEST_SHORTCUT()}>
            <button ref={addRef} className="issue-customer-requests__button" type="button" aria-label={t('Add customer request')} onClick={startRequest}><RequestPlusIcon size={14}/></button>
          </FlowTooltip>
        </div>}
      </div>
      {open && <>
        {composer && <EmbeddedCustomerNeedForm
          key={composer.key}
          data={workspace}
          host="issuePage"
          issueId={issueId}
          initialCustomerId={composer.customerId}
          pendingCustomerName={composer.pendingCustomerName}
          initialSourceUrl={composer.sourceUrl}
          onCancel={() => setComposer(null)}
          onCreated={request => {
            if (composer.pendingCustomerName && request.customerId && !customers.has(request.customerId)) {
              setLocalCustomers(current => [...current, { id: request.customerId, name: composer.pendingCustomerName!.trim(), status: 'active', domains: [], createdAt: request.createdAt, updatedAt: request.createdAt }])
            }
            changed(request)
            onLocalCreated?.(request)
            setComposer(null)
          }}
        />}
        <div className="issue-customer-requests__list">
          {groups.map(group => <EmbeddedCustomerNeedRow key={group.key} data={workspace} group={group} variant="issuePage" defaultExpanded={pendingFor(group)} onChanged={changed} onDeleted={deleted}/>)}
        </div>
      </>}
    </section>
  </>
}
