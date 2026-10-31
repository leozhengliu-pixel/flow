import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '@/i18n/i18n';
import { makeBootstrap } from '@/test/fixtures';
import { MobileTeamsTable } from './mobile-teams-table';

const teams = [
  { id: 'alpha', name: 'Alpha', key: 'ALP', color: '#abc' },
  { id: 'child', name: 'Child', key: 'CHD', color: '#abc' },
  { id: 'parent', name: 'Parent', key: 'PAR', color: '#abc' },
];

function setup(filtered = teams) {
  const onOpen = vi.fn(), onSort = vi.fn();
  const data = makeBootstrap({ teams, teamMembers: [], teamSettings: { child: { parentTeamId: 'parent' } as never } });
  const { container } = render(<MemoryRouter><I18nProvider><MobileTeamsTable data={data} teams={filtered} visibility="Active" memberCounts={new Map([['alpha', 2]])} issueCounts={new Map([['alpha', 21]])} sort="name" descending={false} onOpen={onOpen} onSort={onSort} /></I18nProvider></MemoryRouter>);
  return { container, onOpen, onSort };
}

describe('MobileTeamsTable', () => {
  it('groups only root teams, renders children under their parent and collapses without opening settings', async () => {
    const user = userEvent.setup();
    const { container, onOpen } = setup();
    expect(container.querySelector('.mobile-teams-group')).toHaveTextContent('Active2');
    expect([...container.querySelectorAll('.mobile-team-row')].map(row => row.getAttribute('data-team-key'))).toEqual(['ALP', 'PAR', 'CHD']);
    expect(container.querySelector('[data-team-key=CHD]')).toHaveAttribute('data-depth', '1');
    await user.click(screen.getByRole('button', { name: 'Collapse team' }));
    expect(container.querySelector('[data-team-key=CHD]')).not.toBeInTheDocument();
    expect(onOpen).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Expand team' }));
    expect(container.querySelector('[data-team-key=CHD]')).toBeInTheDocument();
    await user.click(screen.getByText('Parent', { exact: true }));
    expect(onOpen).toHaveBeenCalledWith(teams[2]);
  });

  it('keeps count links and sortable headers functional', async () => {
    const user = userEvent.setup();
    const { container, onSort } = setup();
    const first = container.querySelector('[data-team-key=ALP]') as HTMLElement;
    expect(within(first).getByRole('link', { name: '2' })).toHaveAttribute('href', expect.stringContaining('/teams/ALP/members'));
    expect(within(first).getByRole('link', { name: '21' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Order by Members' }));
    expect(onSort).toHaveBeenCalledWith('members');
    expect(screen.getByRole('columnheader', { name: 'Name' })).toHaveAttribute('aria-sort', 'ascending');
  });

  it('shows a matching child when its parent is filtered out', () => {
    const { container } = setup([teams[1]]);
    expect(container.querySelector('.mobile-teams-group')).toHaveTextContent('Active1');
    expect(container.querySelector('[data-team-key=CHD]')).toHaveAttribute('data-depth', '0');
  });
});
