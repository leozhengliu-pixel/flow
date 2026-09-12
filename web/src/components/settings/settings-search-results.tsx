import { ChevronDown } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'

import { useI18n } from '@/i18n/i18n'

import type { SettingsPageId, TeamSettingsSection } from '@/lib/app-routes'

import type { SettingsSearchResult, SettingsTeamSearchResult } from './settings-search'

type SearchPageMeta = {
  id: SettingsPageId
  title: string
  section: string
  icon: ReactNode
}

export function SettingsSearchResults({
  onSelectPage,
  onSelectResult,
  onSelectTeam,
  pageMeta,
  query,
  results,
  teamResults,
}: {
  onSelectPage: (page: SettingsPageId) => void
  onSelectResult: (result: SettingsSearchResult) => void
  onSelectTeam: (teamKey: string, section?: TeamSettingsSection, targetTitle?: string) => void
  pageMeta: Record<string, SearchPageMeta>
  query: string
  results: SettingsSearchResult[]
  teamResults: SettingsTeamSearchResult[]
}) {
  const { t } = useI18n()
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set())
  const teamGroups = useMemo(() => {
    const grouped = new Map<string, { teamKey: string; teamName: string; results: SettingsTeamSearchResult[]; score: number }>()
    teamResults.forEach(result => {
      const current = grouped.get(result.teamKey)
      if (current) {
        current.results.push(result)
        current.score = Math.max(current.score, result.score)
      } else {
        grouped.set(result.teamKey, {
          teamKey: result.teamKey,
          teamName: result.teamName,
          results: [result],
          score: result.score,
        })
      }
    })
    return [...grouped.values()].sort((left, right) => right.score - left.score)
  }, [teamResults])
  const groups = useMemo(() => {
    const grouped = new Map<SettingsPageId, { page: SearchPageMeta; results: SettingsSearchResult[]; score: number }>()
    results.forEach(result => {
      const page = pageMeta[result.page]
      if (!page) return
      const current = grouped.get(result.page)
      if (current) {
        current.results.push(result)
        current.score = Math.max(current.score, result.score)
      } else {
        grouped.set(result.page, { page, results: [result], score: result.score })
      }
    })
    return [...grouped.values()].sort((left, right) => right.score - left.score)
  }, [pageMeta, results])

  if (!groups.length && !teamResults.length) {
    return <p className="settings-search-empty">{t('No matching settings')}</p>
  }

  return (
    <div className="settings-search-results" aria-label={t('Settings search results')}>
      {teamGroups.map(({ results: teamGroupResults, teamKey, teamName }) => {
        const descendants = teamGroupResults
          .filter(result => result.kind !== 'page')
          .sort((left, right) => {
            if (left.kind !== right.kind) return left.kind === 'section' ? -1 : 1
            return left.key.localeCompare(right.key)
          })
        const collapseKey = `team:${teamKey}`
        const isCollapsed = collapsed.has(collapseKey)
        return (
          <section className="settings-search-group" key={teamKey}>
            <div className="settings-search-group-header">
              <button
                className="settings-search-page settings-search-page--team"
                data-settings-search-result
                onClick={() => onSelectTeam(teamKey)}
                type="button"
              >
                <span className="settings-search-page-title">
                  <span className="settings-search-team-dot" aria-hidden="true" />
                  <Highlight title={t(teamName)} query={query}/>
                </span>
              </button>
              {descendants.length > 0 && (
                <button
                  aria-expanded={!isCollapsed}
                  aria-label={`${isCollapsed ? t('Expand') : t('Collapse')} ${t(teamName)}`}
                  className="settings-search-collapse"
                  onClick={() => setCollapsed(current => {
                    const next = new Set(current)
                    if (next.has(collapseKey)) next.delete(collapseKey)
                    else next.add(collapseKey)
                    return next
                  })}
                  type="button"
                >
                  <ChevronDown size={12}/>
                </button>
              )}
            </div>
            {!isCollapsed && descendants.length > 0 && (
              <div className="settings-search-group-items">
                {descendants.map(result => (
                  <button
                    className={`settings-search-result${result.kind === 'section' ? ' is-section' : ''}`}
                    data-settings-search-result
                    key={result.key}
                    onClick={() => onSelectTeam(result.teamKey, result.section, result.targetTitle)}
                    type="button"
                  >
                    <Highlight title={t(result.title)} query={query}/>
                  </button>
                ))}
              </div>
            )}
          </section>
        )
      })}
      {groups.map(({ page, results: pageResults }) => {
        const descendants = pageResults
          .filter(result => result.kind !== 'page')
          .sort((left, right) => {
            if (left.kind !== right.kind) return left.kind === 'section' ? -1 : 1
            return left.key.localeCompare(right.key)
          })
        const isCollapsed = collapsed.has(page.id)
        return (
          <section className="settings-search-group" key={page.id}>
            <div className="settings-search-group-header">
              <button
                className="settings-search-page"
                data-settings-search-result
                onClick={() => onSelectPage(page.id)}
                type="button"
              >
                <span className="settings-search-page-icon">{page.icon}</span>
                <span className="settings-search-page-title">{t(page.title)}</span>
              </button>
              {descendants.length > 0 && (
                <button
                  aria-expanded={!isCollapsed}
                  aria-label={`${isCollapsed ? t('Expand') : t('Collapse')} ${t(page.title)}`}
                  className="settings-search-collapse"
                  onClick={() => setCollapsed(current => {
                    const next = new Set(current)
                    if (next.has(page.id)) next.delete(page.id)
                    else next.add(page.id)
                    return next
                  })}
                  type="button"
                >
                  <ChevronDown size={12}/>
                </button>
              )}
            </div>
            {!isCollapsed && descendants.length > 0 && (
              <div className="settings-search-group-items">
                {descendants.map(result => (
                  <button
                    className={`settings-search-result${result.kind === 'section' ? ' is-section' : ''}`}
                    data-settings-search-result
                    key={result.key}
                    onClick={() => onSelectResult(result)}
                    type="button"
                  >
                    <Highlight title={t(result.title)} query={query}/>
                  </button>
                ))}
              </div>
            )}
          </section>
        )
      })}
    </div>
  )
}

function Highlight({ query, title }: { query: string; title: string }) {
  const ranges = highlightRanges(title, query)
  if (!ranges.length) return <>{title}</>
  const nodes: ReactNode[] = []
  let cursor = 0
  ranges.forEach(([start, end], index) => {
    if (start > cursor) nodes.push(title.slice(cursor, start))
    nodes.push(<mark key={`${start}:${index}`}>{title.slice(start, end)}</mark>)
    cursor = end
  })
  if (cursor < title.length) nodes.push(title.slice(cursor))
  return <>{nodes}</>
}

function highlightRanges(title: string, query: string): Array<[number, number]> {
  const terms = query
    .trim()
    .split(/\s+/)
    .filter(Boolean)
    .map(term => term.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
  if (!terms.length) return []
  const ranges: Array<[number, number]> = []
  const expression = new RegExp(terms.join('|'), 'gi')
  for (const match of title.matchAll(expression)) {
    const start = match.index ?? 0
    ranges.push([start, start + match[0].length])
  }
  return ranges
}
