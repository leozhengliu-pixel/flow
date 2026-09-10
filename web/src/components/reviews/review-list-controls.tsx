import * as Popover from '@radix-ui/react-popover'
import { ArrowDownWideNarrow, ArrowUpNarrowWide, FolderGit2, GitPullRequest, Link2Off, Sparkles, UserRound, Users } from 'lucide-react'
import { useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import { AppliedFilterBar } from '@/components/filter/applied-filter-bar'
import { DirectoryFilterMenu, type DirectoryFilterGroup } from '@/components/workspace-directory/directory-menus'
import { UserAvatar } from '@/components/ui/user-avatar'
import { Toggle } from '@/components/ui/toggle'
import { SelectControl } from '@/components/ui/select-control'
import { DisplayIcon } from '@/components/ui/view-action-icons'
import { useI18n } from '@/i18n/i18n'
import { personSearchText } from '@/lib/people'
import type { BootstrapData, CodeReview } from '@/types/flow'
import { changeReviewGrouping, defaultReviewDisplay, reviewFieldLabels, reviewPropertyOptions, reviewRepository, reviewStatus, reviewStatusLabels, reviewStatuses, type ReviewDisplay, type ReviewFilter, type ReviewFilterField, type ReviewGrouping, type ReviewOrdering, type ReviewView } from './review-list-model'
import '@/components/workspace-directory/workspace-directory.css'
import './review-list-controls.css'

export function ReviewFilters({ data, items, filters, onChange, filterBarHost }: { data: BootstrapData; items: CodeReview[]; filters: ReviewFilter[]; onChange: (filters: ReviewFilter[]) => void; filterBarHost: HTMLElement | null }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const groups = useMemo<DirectoryFilterGroup[]>(() => {
    const counts = { author: new Map<string, number>(), reviewer: new Map<string, number>(), repository: new Map<string, number>(), status: new Map<string, number>() }
    const users = new Map(data.users.map(user => [user.id, user]))
    const add = (field: keyof typeof counts, id: string) => counts[field].set(id, (counts[field].get(id) ?? 0) + 1)
    for (const review of items) {
      users.set(review.author.id, review.author)
      add('author', review.author.id); add('repository', reviewRepository(review)); add('status', reviewStatus(review))
      for (const id of new Set(review.reviewerIds)) add('reviewer', id)
    }
    const people = (field: 'author' | 'reviewer') => [...users.values()].filter(user => counts[field].has(user.id)).map(user => ({ id: user.id, label: user.displayName, keywords: personSearchText(user), person: user, count: counts[field].get(user.id), meta: String(counts[field].get(user.id)), icon: <UserAvatar name={user.displayName} avatarUrl={user.avatarUrl} className="review-filter-avatar"/> }))
    return [
      { id: 'status', label: t('Status'), icon: <GitPullRequest/>, choices: reviewStatuses.map(id => ({ id, label: t(reviewStatusLabels[id]), count: counts.status.get(id) ?? 0, meta: String(counts.status.get(id) ?? 0) })) },
      { id: 'author', label: t('Author'), icon: <UserRound/>, choices: people('author') },
      { id: 'reviewer', label: t('Reviewers'), icon: <Users/>, choices: people('reviewer') },
      { id: 'repository', label: t('Repository name'), icon: <FolderGit2/>, choices: [...counts.repository].sort(([a], [b]) => a.localeCompare(b)).map(([id, count]) => ({ id, label: id, count, meta: String(count), icon: <FolderGit2 size={14}/> })) },
      { id: 'quick', label: t('Quick to review'), icon: <Sparkles/> },
      { id: 'missingIssue', label: t('Missing issue'), icon: <Link2Off/> },
    ]
  }, [items, data.users, t])
  const choice = (field: string, id: string, checked: boolean) => {
    const key = field as ReviewFilterField, current = filters.find(filter => filter.id === key)
    const option = groups.find(group => group.id === key)?.choices?.find(option => option.id === id) ?? { id, label: t(id === 'true' ? 'Yes' : 'No') }
    const values = [...(current?.values ?? []).filter(value => value.id !== id), ...(checked ? [{ id, label: option.label }] : [])]
    const next = { id: key, fieldLabel: reviewFieldLabels[key], operator: current?.operator ?? 'is' as const, values }
    onChange([...filters.filter(filter => filter.id !== key), ...(values.length ? [next] : [])])
  }
  return <>
    <DirectoryFilterMenu menuClassName="review-filter-menu" triggerClassName="reviews-icon-button" open={open} onOpenChange={setOpen} groups={groups} showAdvanced={false} selected={Object.fromEntries(filters.map(filter => [filter.id, new Set(filter.values.map(value => value.id))]))} onChoice={choice} onDirect={field => { choice(field, 'true', true); setOpen(false) }}/>
    {filters.length > 0 && filterBarHost && createPortal(<div className="review-applied-filters"><AppliedFilterBar ariaLabel="Review filters" filters={filters.map(filter => ({ ...filter, fieldLabel: t(reviewFieldLabels[filter.id]) }))} translate={t} onAdd={() => setOpen(true)} onClear={() => onChange([])} onRemove={filter => onChange(filters.filter(item => item.id !== filter.id))} onOperatorChange={(filter, operator) => onChange(filters.map(item => item.id === filter.id ? { ...item, operator } : item))} onValuesChange={(filter, values) => onChange(filters.flatMap(item => item.id === filter.id ? values.length ? [{ ...item, values }] : [] : [item]))} optionsFor={filter => groups.find(group => group.id === filter.id)?.choices ?? [{ id: 'true', label: t('Yes') }, { id: 'false', label: t('No') }]}/></div>, filterBarHost)}
  </>
}

export function ReviewDisplayMenu({ display, view, onChange }: { display: ReviewDisplay; view: ReviewView; onChange: (display: ReviewDisplay) => void }) {
  const { t } = useI18n()
  const grouping = [{ value: 'none', label: 'No grouping' }, ...(view === 'for-you' ? [{ value: 'focus', label: 'Focus' }] : []), { value: 'status', label: 'Status' }, { value: 'author', label: 'Author' }, { value: 'repository', label: 'Repository' }]
  const ordering = [...(display.grouping === 'focus' ? [{ value: 'importance', label: 'Importance' }] : []), { value: 'name', label: 'Name' }, { value: 'author', label: 'Author' }, { value: 'status', label: 'Status' }, { value: 'opened', label: 'Opened' }, { value: 'updated', label: 'Updated' }]
  return <Popover.Root><Popover.Trigger asChild><button type="button" className="reviews-icon-button" aria-label={t('Display options')}><DisplayIcon/></button></Popover.Trigger><Popover.Portal><Popover.Content data-flow-motion="floating" className="review-list-options" align="end" sideOffset={5} collisionPadding={8}>
    <div className="review-list-options__section">
      <div className="review-list-options__row"><span>{t('Grouping')}</span><SelectControl label={t('Grouping')} value={display.grouping} options={grouping.map(option => ({ ...option, label: t(option.label) }))} onChange={value => onChange(changeReviewGrouping(display, value as ReviewGrouping))}/></div>
      <div className="review-list-options__row"><span>{t('Ordering')}</span>{display.ordering !== 'importance' && <button type="button" className="review-list-options__direction" aria-label={t(display.descending ? 'Sort ascending' : 'Sort descending')} onClick={() => onChange({ ...display, descending: !display.descending })}>{display.descending ? <ArrowDownWideNarrow size={14}/> : <ArrowUpNarrowWide size={14}/>}</button>}<SelectControl label={t('Ordering')} value={display.ordering} options={ordering.map(option => ({ ...option, label: t(option.label) }))} onChange={value => onChange({ ...display, ordering: value as ReviewOrdering, descending: value === 'opened' || value === 'updated' })}/></div>
    </div>
    <div className="review-list-options__section">
      <div className="review-list-options__row"><span>{t('Closed reviews')}</span><SelectControl label={t('Closed reviews')} value={display.closed} options={[{ value: 'all', label: 'All' }, { value: 'day', label: 'Past day' }, { value: 'week', label: 'Past week' }, { value: 'month', label: 'Past month' }, { value: 'none', label: 'None' }].map(option => ({ ...option, label: t(option.label) }))} onChange={value => onChange({ ...display, closed: value as ReviewDisplay['closed'] })}/></div>
      <div className="review-list-options__row"><span>{t('Show drafts')}</span><Toggle label={t('Show drafts')} checked={display.showDrafts} onChange={showDrafts => onChange({ ...display, showDrafts })}/></div>
      {view === 'for-you' && <div className="review-list-options__row"><span>{t('Show GitHub team reviews')}</span><Toggle label={t('Show GitHub team reviews')} checked={display.showTeams} onChange={showTeams => onChange({ ...display, showTeams })}/></div>}
    </div>
    <div className="review-list-options__section"><h4>{t('Display properties')}</h4><div className="review-list-options__properties">{reviewPropertyOptions.map(property => <button type="button" key={property.id} aria-pressed={display.properties.includes(property.id)} onClick={() => onChange({ ...display, properties: display.properties.includes(property.id) ? display.properties.filter(id => id !== property.id) : [...display.properties, property.id] })}>{t(property.label)}</button>)}</div></div>
    {JSON.stringify(display) !== JSON.stringify(defaultReviewDisplay(view)) && <footer><button type="button" onClick={() => onChange(defaultReviewDisplay(view))}>{t('Reset to view default')}</button></footer>}
  </Popover.Content></Popover.Portal></Popover.Root>
}
