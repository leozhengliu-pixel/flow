import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { BootstrapData, FlowDocument } from '@/types/flow'

const api = vi.hoisted(() => ({
  fetchTeamResources: vi.fn(),
  setTeamMembership: vi.fn(),
  pinTeamResource: vi.fn(),
  updateTeamResource: vi.fn(),
  deleteTeamResource: vi.fn(),
  createTeamResourceSection: vi.fn(),
  updateTeamResourceSection: vi.fn(),
  deleteTeamResourceSection: vi.fn(),
}))
const confirm = vi.hoisted(() => vi.fn().mockResolvedValue(false));
vi.mock('@/components/ui/action-dialog-service', () => ({ confirmAction: confirm }));

vi.mock('@/lib/api', async importOriginal => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  ...api,
}))

import { TeamOverviewPage } from './team-overview-page'

function renderPage(view: "overview" | "documents" | "loops" | "members", overrides: Partial<BootstrapData> = {}) {
  const data = makeBootstrap({
    documents: [],
    favorites: [],
    members: [viewer, ...makeBootstrap().users.filter(user => user.id !== viewer.id)].map(user => ({ user, role: 'member' as const, status: 'active' as const, joinedAt: '' })),
    subscriptions: [],
    teamMembers: [{ teamId: 'team-1', userId: viewer.id, role: 'owner', joinedAt: '2026-08-01T00:00:00Z' }],
    teamSettings: {
      'team-1': { teamId: 'team-1', description: '' } as unknown as BootstrapData['teamSettings'][string],
    },
    ...overrides,
  })
  return render(
    <I18nProvider>
      <TeamOverviewPage
        data={data}
        onNavigate={vi.fn()}
        onOpenSidebar={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
        team={data.teams[0]}
        view={view}
      />
    </I18nProvider>,
  )
}

function renderOverview(overrides: Partial<BootstrapData> = {}) {
  return renderPage("overview", overrides)
}

describe('team overview', () => {
  beforeEach(() => {
    confirm.mockReset().mockResolvedValue(false);
    localStorage.clear();
    for (const mock of Object.values(api)) mock.mockReset().mockResolvedValue(undefined);
    api.fetchTeamResources.mockReset().mockResolvedValue({
      resources: [],
      sections: [],
    })
    api.setTeamMembership.mockReset().mockResolvedValue(undefined)
  })

  const section = { id: 'section-1', teamId: 'team-1', name: 'Plans', position: 1, createdAt: '', updatedAt: '' };
  const document: FlowDocument = { id: 'doc-1', title: 'Roadmap', slugId: 'roadmap', content: '', creator: viewer, teamIds: ['team-1'], projectIds: [], subscriberIds: [], favorite: false, revisions: [], createdAt: '2026-09-07T00:00:00Z', updatedAt: '2026-09-07T00:00:00Z' };

  it('confirms deleting a nonempty section and preserves it on cancel', async () => {
    const user = userEvent.setup();
    api.fetchTeamResources.mockResolvedValue({ resources: [{ id: 'pin-1', resourceType: 'document', resourceId: document.id, title: document.title, sectionId: section.id, position: 0 }], sections: [section] });
    renderOverview({ documents: [document] });
    await user.click(await screen.findByRole('button', { name: 'Open menu Plans' }));
    await user.click(screen.getByRole('menuitem', { name: 'Delete…' }));
    await waitFor(() => expect(confirm).toHaveBeenCalledWith('Delete "Plans"?', expect.objectContaining({ description: 'Resources in this section will stay pinned to the page.' })));
    expect(api.deleteTeamResourceSection).not.toHaveBeenCalled();
  });

  it('moves a dragged resource into the target section', async () => {
    api.fetchTeamResources.mockResolvedValue({ resources: [{ id: 'pin-1', resourceType: 'document', resourceId: document.id, title: document.title, sectionId: '', position: 0 }], sections: [section] });
    const { container } = renderOverview({ documents: [document] });
    await screen.findByText('Plans');
    fireEvent.drop(container.querySelector('.team-resource-section.is-named')!, { dataTransfer: { getData: (type: string) => type === 'application/x-flow-team-resource' ? 'pin-1' : '' } });
    await waitFor(() => expect(api.updateTeamResource).toHaveBeenCalledWith('team-1', 'pin-1', { sectionId: 'section-1', position: 0 }));
  });

  it('opens every level by hover and pins an existing document to the originating section', async () => {
    const user = userEvent.setup();
    api.fetchTeamResources.mockResolvedValue({ resources: [], sections: [section] });
    renderOverview({ documents: [document] });
    await user.click(await screen.findByRole('button', { name: 'Open menu Plans' }));
    expect(screen.getByRole('menuitem', { name: 'Copy link' })).toBeVisible();
    await user.hover(screen.getByRole('menuitem', { name: 'Add resources' }));
    fireEvent.pointerMove(await screen.findByRole('menuitem', { name: 'Existing documents' }), { pointerType: 'mouse' });
    fireEvent.click(await screen.findByRole('menuitemcheckbox', { name: 'Roadmap' }));
    await waitFor(() => expect(api.pinTeamResource).toHaveBeenCalledWith('team-1', expect.objectContaining({ sectionId: 'section-1', resourceId: 'doc-1' })));
    expect(screen.getByRole('menuitemcheckbox', { name: 'Roadmap' })).toBeVisible();
  });

  it('moves an existing pin from another section rather than creating a duplicate', async () => {
    const user = userEvent.setup();
    api.fetchTeamResources.mockResolvedValue({ resources: [{ id: 'pin-1', teamId: 'team-1', resourceType: 'document', resourceId: document.id, title: document.title, sectionId: '', position: 0 }], sections: [section] });
    renderOverview({ documents: [document] });
    await user.click(await screen.findByRole('button', { name: 'Open menu Plans' }));
    await user.hover(screen.getByRole('menuitem', { name: 'Add resources' }));
    fireEvent.pointerMove(await screen.findByRole('menuitem', { name: 'Existing documents' }), { pointerType: 'mouse' });
    fireEvent.click(await screen.findByRole('menuitemcheckbox', { name: 'Roadmap' }));
    await waitFor(() => expect(api.updateTeamResource).toHaveBeenCalledWith('team-1', 'pin-1', { sectionId: 'section-1' }));
    expect(api.pinTeamResource).not.toHaveBeenCalled();
  });

  it('allows unpinning an already selected document', async () => {
    const user = userEvent.setup();
    api.fetchTeamResources.mockResolvedValue({ resources: [{ id: 'pin-1', teamId: 'team-1', resourceType: 'document', resourceId: document.id, title: document.title, sectionId: section.id, position: 0 }], sections: [section] });
    renderOverview({ documents: [document] });
    await user.click(await screen.findByRole('button', { name: 'Open menu Plans' }));
    await user.hover(screen.getByRole('menuitem', { name: 'Add resources' }));
    fireEvent.pointerMove(await screen.findByRole('menuitem', { name: 'Existing documents' }), { pointerType: 'mouse' });
    const choice = await screen.findByRole('menuitemcheckbox', { name: 'Roadmap' });
    expect(choice).toHaveAttribute('aria-checked', 'true');
    fireEvent.click(choice);
    await waitFor(() => expect(api.deleteTeamResource).toHaveBeenCalledWith('team-1', 'pin-1'));
  });

  it('persists collapsed sections and supports Alt toggling all sections', async () => {
    api.fetchTeamResources.mockResolvedValue({ resources: [], sections: [section, { ...section, id: 'section-2', name: 'Notes' }] });
    renderOverview();
    const controls = await screen.findAllByRole('button', { name: 'Collapse section' });
    fireEvent.click(controls[0], { altKey: true });
    expect(screen.getAllByRole('button', { name: 'Expand section' })).toHaveLength(2);
    expect(JSON.parse(localStorage.getItem('flow:team:team-1:collapsed-sections') ?? '[]')).toEqual(['section-1', 'section-2']);
  });

  it('does not save a canceled rename on blur', async () => {
    const user = userEvent.setup();
    api.fetchTeamResources.mockResolvedValue({ resources: [], sections: [section] });
    renderOverview();
    await user.click(await screen.findByRole('button', { name: 'Open menu Plans' }));
    await user.click(screen.getByRole('menuitem', { name: 'Rename…' }));
    const input = await screen.findByRole('textbox', { name: 'Rename section' });
    await user.clear(input); await user.type(input, 'Changed'); await user.keyboard('{Escape}');
    expect(api.updateTeamResourceSection).not.toHaveBeenCalled();
    expect(screen.getByText('Plans')).toBeVisible();
  });

  it('commits a new section on blur only once', async () => {
    const user = userEvent.setup(); renderOverview();
    await user.click(screen.getByRole('button', { name: 'Add section' }));
    const input = screen.getByRole('textbox', { name: 'Section name' });
    await user.type(input, 'Plans'); fireEvent.blur(input);
    await waitFor(() => expect(api.createTeamResourceSection).toHaveBeenCalledWith('team-1', 'Plans'));
    expect(api.createTeamResourceSection).toHaveBeenCalledTimes(1);
  });

  it('renders the measured team header, tabs, resources, members, and shortcuts', async () => {
    renderOverview()
    await waitFor(() => expect(api.fetchTeamResources).toHaveBeenCalledWith('team-1'))

    expect(screen.getByRole('heading', { level: 2, name: 'Test team' })).toBeVisible()
    expect(screen.getByRole('navigation', { name: 'Team views' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Overview' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('button', { name: 'Choose team icon' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Team resources' })).toBeVisible()
    expect(screen.getByText('Add documents and links. Organize by creating sections.')).toBeVisible()
    expect(screen.getByText('Go to')).toBeVisible()
    expect(screen.getByRole('link', { name: 'Team settings' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Cycles' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Views' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Loops' })).toHaveAttribute('href', '/workspace/team/TST/loops')
  })

  it('hides the Loops tab when the workspace flag is off', async () => {
    renderOverview({
      workspaceSettings: { ...makeBootstrap().workspaceSettings, featureFlags: { loops: false } },
    })
    await waitFor(() => expect(api.fetchTeamResources).toHaveBeenCalledWith('team-1'))
    expect(screen.queryByRole('link', { name: 'Loops' })).not.toBeInTheDocument()
  })

  it('uses the Linear resource menu and creates sections inline', async () => {
    const user = userEvent.setup()
    renderOverview()

    await user.click(screen.getByRole('button', { name: 'Add resources' }))
    expect(screen.getByRole('menuitem', { name: 'New document' })).toBeVisible()
    expect(screen.getByRole('menuitem', { name: 'Existing documents' })).toBeVisible()
    expect(screen.getByRole('menuitem', { name: 'New link…' })).toBeVisible()
    await user.keyboard('{Escape}')

    await user.click(screen.getByRole('button', { name: 'Add section' }))
    expect(screen.getByRole('textbox', { name: 'Section name' })).toBeVisible()
    expect(screen.queryByRole('dialog', { name: 'Add section' })).not.toBeInTheDocument()
  })

  it('opens the member picker and persists selected workspace members', async () => {
    const user = userEvent.setup()
    renderOverview()

    await user.click(screen.getByRole('button', { name: 'Add members' }))
    expect(screen.getByRole('dialog', { name: 'Add members to Test team' })).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Select members' }))
    await user.click(screen.getByRole('menuitemcheckbox', { name: 'Teammate' }))
    await user.keyboard('{Escape}')
    await user.click(screen.getByRole('button', { name: 'Add members' }))

    await waitFor(() =>
      expect(api.setTeamMembership).toHaveBeenCalledWith(
        'workspace',
        'team-1',
        'user-2',
        true,
        'member',
      ),
    )
  })

  it('provides the complete team document filter and display controls', async () => {
    const user = userEvent.setup()
    const project = { ...makeBootstrap().projects[0], id: 'project-docs', name: 'Website', teamIds: ['team-1'] }
    const pinned = { id: 'pin-doc', teamId: 'team-1', resourceType: 'document', resourceId: document.id, title: document.title, position: 0, createdAt: '', updatedAt: '' }
    api.fetchTeamResources.mockResolvedValue({ resources: [pinned], sections: [] })
    renderPage('documents', { documents: [{ ...document, projectIds: [project.id] }, { ...document, id: 'doc-team', slugId: 'team-notes', title: 'Team notes' }], projects: [project] })

    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    expect(screen.getByRole('menuitem', { name: 'Advanced filter' })).toBeVisible()
    for (const label of ['Creator', 'Owner', 'Project', 'Cycle', 'Dates']) expect(screen.getByRole('menuitem', { name: label })).toBeVisible()
    await user.keyboard('{Escape}')

    await user.click(screen.getByRole('button', { name: 'Display options' }))
    expect(screen.getByRole('combobox', { name: 'Grouping' })).toHaveValue('project')
    expect(screen.getByRole('combobox', { name: 'Ordering' })).toHaveValue('name')
    expect(screen.getByRole('switch', { name: 'Show inactive projects' })).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByRole('switch', { name: 'Show only my projects' })).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByRole('button', { name: 'Owner', pressed: true })).toBeVisible()
    await user.keyboard('{Escape}')

    expect(screen.getByText('Team documents')).toBeVisible()
    expect(screen.getByRole('link', { name: 'Website' })).toHaveAttribute('href', expect.stringContaining('/project/'))
    expect(await screen.findByLabelText('Pinned to team overview')).toBeVisible()
  })

  it('makes document pinning and advanced filter matching functional', async () => {
    const user = userEvent.setup()
    api.fetchTeamResources.mockResolvedValue({ resources: [], sections: [] })
    renderPage('documents', { documents: [document] })
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(screen.getByRole('menuitem', { name: 'Pin to team overview' }))
    await waitFor(() => expect(api.pinTeamResource).toHaveBeenCalledWith('team-1', expect.objectContaining({ resourceId: 'doc-1' })))

    await user.click(screen.getByRole('button', { name: 'Add filter' }))
    await user.click(screen.getByRole('menuitem', { name: 'Advanced filter' }))
    const advanced = screen.getByRole('dialog', { name: 'Advanced filter' })
    await user.click(within(advanced).getByRole('button', { name: 'Any filter' }))
    expect(within(advanced).getByRole('button', { name: 'Any filter' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('sorts members, configures columns, and virtualizes a long independently scrollable list', async () => {
    const user = userEvent.setup()
    const extraUsers = Array.from({ length: 100 }, (_, index) => ({ ...viewer, id: `member-${index}`, name: `person-${String(index).padStart(3, '0')}`, displayName: `Person ${String(index).padStart(3, '0')}`, email: `person-${index}@example.com` }))
    const teamMembers = extraUsers.map((person, index) => ({ teamId: 'team-1', userId: person.id, role: index === 0 ? 'owner' as const : 'member' as const, joinedAt: '' }))
    renderPage('members', { users: extraUsers, members: extraUsers.map(user => ({ user, role: 'member' as const, status: 'active' as const, joinedAt: '' })), teamMembers })

    const list = screen.getByRole('list', { name: 'Team members' })
    expect(list).toHaveClass('team-members-scroll')
    expect(globalThis.document.querySelectorAll('.team-member-row').length).toBeLessThan(100)
    await user.click(screen.getByRole('button', { name: 'Display options' }))
    await user.click(screen.getByRole('button', { name: 'Email', pressed: true }))
    await user.click(screen.getByRole('button', { name: 'Display options' }))
    expect(screen.queryByRole('button', { name: 'Email' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'A-Z' }))
    expect(screen.getByRole('button', { name: 'Z-A' })).toBeVisible()
  })

  it('searches add-member candidates by employee id and can add a team owner', async () => {
    const user = userEvent.setup()
    const employee = { ...viewer, id: 'employee-user', userId: '25062215', name: 'liuzheng', displayName: '刘峥', email: 'liu@example.com' }
    renderPage('overview', { users: [viewer, employee], members: [viewer, employee].map(person => ({ user: person, role: 'member' as const, status: 'active' as const, joinedAt: '' })) })
    await user.click(screen.getByRole('button', { name: 'Add members' }))
    await user.click(screen.getByRole('button', { name: 'Select members' }))
    await user.type(screen.getByRole('textbox', { name: 'Search members…' }), '25062215')
    await user.click(screen.getByRole('menuitemcheckbox', { name: '刘峥' }))
    await user.keyboard('{Escape}')
    await user.click(screen.getByRole('button', { name: 'Team owner' }))
    await user.click(screen.getByRole('button', { name: 'Add members' }))
    await waitFor(() => expect(api.setTeamMembership).toHaveBeenCalledWith('workspace', 'team-1', 'employee-user', true, 'owner'))
  })

  it('separates team and workspace roles, shows invitations, and applies visibility controls', async () => {
    const user = userEvent.setup()
    renderPage('members', {
      viewerRole: 'owner',
      members: [{ user: viewer, role: 'owner', status: 'active', joinedAt: '' }],
      invitations: [{ id: 'invite-team', workspaceId: 'workspace-1', email: 'invited@example.com', role: 'member', teamIds: ['team-1'], status: 'pending', inviterId: viewer.id, expiresAt: '', createdAt: '' }],
    })
    expect(screen.getByText('Team owner')).toBeVisible()
    expect(screen.getByText('Workspace owner')).toBeVisible()
    expect(screen.getAllByText('invited@example.com')).toHaveLength(2)
    await user.click(screen.getByRole('button', { name: 'Display options' }))
    await user.click(screen.getByRole('switch', { name: 'Show invited' }))
    expect(screen.queryByText('invited@example.com')).not.toBeInTheDocument()
  })

  it('keeps management controls hidden while preserving a member self-leave menu', async () => {
    const user = userEvent.setup()
    const settings = { ...makeBootstrap().teamSettings?.['team-1'], teamId: 'team-1', memberPermission: 'owners' } as BootstrapData['teamSettings'][string]
    renderPage('members', { viewerRole: 'member', teamSettings: { 'team-1': settings }, members: [{ user: viewer, role: 'member', status: 'active', joinedAt: '' }], teamMembers: [{ teamId: 'team-1', userId: viewer.id, role: 'member', joinedAt: '' }] })
    expect(screen.queryByRole('button', { name: 'Add a member' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    expect(screen.getByRole('menuitem', { name: 'Leave team' })).toBeVisible()
    expect(screen.queryByRole('menuitem', { name: 'Make team owner' })).not.toBeInTheDocument()
  })
})
