/**
 * LS-0096 AutomationsPage IA — dual Automations/Loops chrome, search, view prefs stub.
 */
import { useMemo, useState } from 'react'
import { ChevronRight, Plus, Search } from 'lucide-react'
import { AutomationsEmptyStateIcon } from '@/components/automation/automations-empty-state-icon'
import {
  ContentViewContainer,
  ContentViewHeaderTitle,
} from '@/components/content-view'
import { automationsPath, loopsPath, automationNewPath } from '@/lib/app-routes'
import { useStoredState } from '@/hooks/use-stored-state'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, WorkflowDefinition } from '@/types/flow'
import './automations-page.css'

export type AutomationsPageProps = {
  data: BootstrapData
  workflows: WorkflowDefinition[]
  onNavigate: (path: string) => void
}

export function AutomationsPage({ data, workflows, onNavigate }: AutomationsPageProps) {
  const { t, formatDate } = useI18n()
  const [query, setQuery] = useState('')
  const [order, setOrder] = useStoredState<'name' | 'recent'>('automations.viewOrder', 'name', 'local')

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    const rows = workflows.filter(item => {
      if (!q) return true
      return (
        item.name.toLowerCase().includes(q) ||
        (item.description ?? '').toLowerCase().includes(q) ||
        item.trigger.toLowerCase().includes(q)
      )
    })
    return [...rows].sort((a, b) => {
      if (order === 'recent') {
        return (b.lastRunAt ?? b.updatedAt).localeCompare(a.lastRunAt ?? a.updatedAt)
      }
      return a.name.localeCompare(b.name)
    })
  }, [order, query, workflows])

  const slug = data.workspace.urlKey

  return (
    <ContentViewContainer
      className="flow-automations-page"
      framed={false}
      data-automations-page=""
      aria-label={t('Automations')}
    >
      <header className="flow-automations-page__chrome">
        <ContentViewHeaderTitle title={t('Automations')} />
        <nav className="flow-automations-page__tabs" aria-label={t('Automation surfaces')}>
          <button type="button" className="is-active" aria-current="page">
            {t('Automations')}
          </button>
          <button type="button" onClick={() => onNavigate(loopsPath(slug))}>
            {t('Loops')}
          </button>
        </nav>
        <div className="flow-automations-page__tools">
          <label className="flow-automations-page__search">
            <Search size={14} aria-hidden />
            <input
              value={query}
              onChange={event => setQuery(event.target.value)}
              placeholder={t('Find automations…')}
              aria-label={t('Find automations…')}
            />
          </label>
          <select
            aria-label={t('Order')}
            value={order}
            onChange={event => setOrder(event.target.value as 'name' | 'recent')}
          >
            <option value="name">{t('Alphabetical')}</option>
            <option value="recent">{t('Recency')}</option>
          </select>
          <button
            type="button"
            className="flow-automations-page__new"
            onClick={() => onNavigate(automationNewPath(slug))}
          >
            <Plus size={14} />
            {t('New automation')}
          </button>
        </div>
      </header>

      <div className="flow-automations-page__list" role="list">
        {filtered.map(item => (
          <a
            className="flow-automations-page__row"
            role="listitem"
            href={`/${slug}/automation/${encodeURIComponent(item.id)}`}
            onClick={event => {
              event.preventDefault()
              onNavigate(`/${slug}/automation/${encodeURIComponent(item.id)}`)
            }}
            key={item.id}
          >
            <div className={`flow-automations-page__dot ${item.enabled ? 'is-on' : ''}`} />
            <div className="flow-automations-page__main">
              <strong data-i18n-ignore>{item.name}</strong>
              <small>
                {item.trigger} · {item.lastRunStatus || t('Never run')}
              </small>
            </div>
            <span className="flow-automations-page__meta">
              {item.lastRunAt ? formatDate(item.lastRunAt, { dateStyle: 'medium' }) : ''}
            </span>
            <ChevronRight size={15} aria-hidden />
          </a>
        ))}
      </div>

      {!filtered.length && (
        <div className="flow-automations-page__empty">
          <AutomationsEmptyStateIcon />
          <h3>{workflows.length ? t('No matching automations') : t('No automations yet')}</h3>
          <p>
            {workflows.length
              ? t('Try a different search.')
              : t('Create an automation to automate repetitive work.')}
          </p>
          {!workflows.length && (
            <button type="button" onClick={() => onNavigate(automationNewPath(slug))}>
              {t('New automation')}
            </button>
          )}
          <button
            type="button"
            className="flow-automations-page__link"
            onClick={() => onNavigate(loopsPath(slug))}
          >
            {t('Open Loops')}
          </button>
        </div>
      )}

      <p className="flow-automations-page__footnote">
        {t('Agent automations (Loops) live under Loops. Durable workflow rules stay here.')}
      </p>
    </ContentViewContainer>
  )
}

export default AutomationsPage
