import { Fragment, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { ViewGlyph } from '@/components/views/view-icon-picker';
import { useI18n } from '@/i18n/i18n';
import { settingsPath, teamIssuesPath } from '@/lib/app-routes';
import { teamContentAccessible } from '@/lib/team-access';
import type { BootstrapData, Team } from '@/types/flow';

type Sort = 'name' | 'members' | 'issues';

export function MobileTeamsTable({ data, teams, visibility, memberCounts, issueCounts, sort, descending, filtered = false, onSort, onOpen }: {
  data: BootstrapData;
  teams: Team[];
  visibility: string;
  memberCounts: Map<string, number>;
  issueCounts: Map<string, number>;
  sort: string;
  descending: boolean;
  filtered?: boolean;
  onSort: (key: Sort) => void;
  onOpen: (team: Team) => void;
}) {
  const { t } = useI18n();
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const parentId = (team: Team) => data.teamSettings?.[team.id]?.parentTeamId;
  const ids = new Set(teams.map(team => team.id));
  const roots = teams.filter(team => !ids.has(parentId(team) ?? ''));
  const count = (value: number | undefined) => value ? value.toLocaleString() : '-';
  const rows = (items: Team[], depth = 0, ancestors = new Set<string>()): ReactNode => items.map(team => {
    if (ancestors.has(team.id)) return null;
    const children = teams.filter(child => parentId(child) === team.id);
    const expanded = !collapsed.has(team.id);
    const nextAncestors = new Set([...ancestors, team.id]);
    return <Fragment key={team.id}>
      <div className="mobile-team-row" data-team-key={team.key} data-depth={depth} role="link" tabIndex={0} onClick={() => onOpen(team)} onKeyDown={event => { if (event.key === 'Enter' && event.target === event.currentTarget) onOpen(team); }}>
        <span className="mobile-team-name-cell" style={{ paddingLeft: 30 + depth * 27 }}>
          {depth > 0 && <span className="mobile-team-branch" aria-hidden="true"><svg width="10" height="9" viewBox="0 0 10 9" fill="currentColor"><path d="M0 0h1v1c0 2.5 2.212 3.546 2.212 3.546L9.737 8.06c.568.306.094 1.186-.474.88l-6.48-3.488S0 4 0 1V0Z" /></svg></span>}
          <span className="mobile-team-icon"><ViewGlyph color={team.color} icon={team.icon || 'Team'} /></span>
          <strong>{team.name}</strong><small>{team.key}</small>
          {children.length > 0 && <button className="mobile-team-collapse" type="button" aria-label={t(expanded ? 'Collapse team' : 'Expand team')} aria-expanded={expanded} onClick={event => { event.stopPropagation(); setCollapsed(previous => { const next = new Set(previous); if (next.has(team.id)) next.delete(team.id); else next.add(team.id); return next; }); }}>
            <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor" style={{ transform: expanded ? 'rotate(90deg)' : undefined }}><path d="M6.75194 10.6239C6.41861 10.8183 6 10.5779 6 10.192V5.80802C6 5.42212 6.41861 5.18169 6.75194 5.37613L10.5096 7.56811C10.8404 7.76105 10.8404 8.23895 10.5096 8.43189L6.75194 10.6239Z" /></svg>
          </button>}
        </span>
        <span><Link className="mobile-team-count" to={settingsPath(data.workspace.urlKey, 'team', team.key, 'members')} onClick={event => event.stopPropagation()}><span>{count(memberCounts.get(team.id))}</span></Link></span>
        <span>{teamContentAccessible(data, team) ? <Link className="mobile-team-count" to={teamIssuesPath(data.workspace.urlKey, team.key)} onClick={event => event.stopPropagation()}><span data-empty={!issueCounts.get(team.id)}>{count(issueCounts.get(team.id))}</span></Link> : <span className="mobile-team-count"><span data-empty={!issueCounts.get(team.id)}>{count(issueCounts.get(team.id))}</span></span>}</span>
      </div>
      {expanded && rows(children, depth + 1, nextAncestors)}
    </Fragment>;
  });
  return <div className="mobile-teams-scroll">
    <div className="mobile-teams-table">
      <div className="mobile-teams-columns">
        {([['name', 'Name'], ['members', 'Members'], ['issues', 'Issues']] as const).map(([key, label]) => <span key={key} role="columnheader" aria-sort={sort === key ? descending ? 'descending' : 'ascending' : 'none'}><button type="button" data-sort={key} aria-label={`${t('Order by')} ${t(label)}`} onClick={() => onSort(key)}><span>{t(label)}</span><span className="mobile-team-sort-icon" style={{ visibility: sort === key ? 'visible' : 'hidden' }}><MobileTeamSortGlyph reversed={sort === key && descending} /></span></button></span>)}
      </div>
      {roots.length > 0 && <div className="mobile-teams-group"><strong>{t(visibility)}</strong><span>{roots.length}</span></div>}
      {rows(roots)}
      {!teams.length && <div className="settings-team-members-empty">{t(filtered ? 'No matching teams' : visibility === 'Retired' ? 'No retired teams' : 'No active teams')}</div>}
    </div>
  </div>;
}

function MobileTeamSortGlyph({ reversed }: { reversed: boolean }) {
  return <svg aria-hidden="true" width="12" height="12" viewBox="0 0 16 16" fill="currentColor" style={{ transform: reversed ? 'rotate(180deg)' : undefined }}>
    <path d="M11.5361 10.2745C11.8024 10.0029 11.8249 9.56807 11.5762 9.26961C11.3275 8.97139 10.8961 8.91526 10.5811 9.12801L10.5195 9.17391L8.00001 11.2735L5.48048 9.17391L5.41895 9.12801C5.10388 8.91526 4.67252 8.97139 4.42384 9.26961C4.17512 9.56807 4.19758 10.0029 4.46387 10.2745L4.51954 10.3263L7.51954 12.8263C7.79767 13.058 8.20234 13.058 8.48048 12.8263L11.4805 10.3263L11.5361 10.2745Z" />
    <path d="M8.75 12.25C8.75 12.6642 8.41421 13 8 13C7.58579 13 7.25 12.6642 7.25 12.25L7.25 3.75C7.25 3.33579 7.58579 3 8 3C8.41421 3 8.75 3.33579 8.75 3.75V12.25Z" />
  </svg>;
}
