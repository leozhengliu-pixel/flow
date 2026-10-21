import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '@/i18n/i18n';
import { PersonalConnectorSettings } from './personal-connectors';

const { request, toast } = vi.hoisted(() => ({
  request: vi.fn(),
  toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn(), loading: vi.fn(() => 'pending'), dismiss: vi.fn() }),
}));
vi.mock('@/lib/api-client', () => ({ request, jsonRequest: (method: string, body: unknown) => ({ method, body: JSON.stringify(body) }) }));
vi.mock('@/lib/toast', () => ({ toast }));

const own = { id: 'wiki', name: 'DeepWiki', kind: 'mcp', status: 'pending', shared: false, ownerId: 'me', url: 'https://mcp.deepwiki.com/mcp', tools: ['ask_question', 'read_contents', 'read_structure'], connectedAt: '2026-10-20T00:00:00Z' };

function respond(policies: unknown[], connect: unknown = { connected: true, policy: own }) {
  request.mockImplementation(async (url: string, init?: { method?: string }) => {
    if (url === '/api/application-policies' && !init?.method) return policies;
    if (url.endsWith('/connect')) return connect;
    if (url.endsWith('/authorization')) return { authorized: false, customHeaders: false };
    return {};
  });
}

async function openMenu() {
  const trigger = await screen.findByRole('button', { name: 'Connector options' });
  fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false, pointerType: 'mouse' });
  return screen.findByRole('menu');
}

describe('personal MCP connectors', () => {
  beforeEach(() => { vi.clearAllMocks(); });

  it('shows the empty state with the add pill', async () => {
    respond([]);
    render(<I18nProvider><PersonalConnectorSettings viewerId="me" /></I18nProvider>);
    expect(await screen.findByText('No connectors added')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add connector' })).toBeInTheDocument();
  });

  it('lists only the viewer’s own connectors with their count, tools and URL', async () => {
    respond([own, { ...own, id: 'other', name: 'Other', ownerId: 'someone' }, { ...own, id: 'ws', name: 'Workspace', shared: true }]);
    render(<I18nProvider><PersonalConnectorSettings viewerId="me" /></I18nProvider>);
    expect(await screen.findByText('1 connector added')).toBeInTheDocument();
    expect(screen.getByText('DeepWiki')).toBeInTheDocument();
    expect(screen.queryByText('Other')).not.toBeInTheDocument();
    expect(screen.queryByText('Workspace')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '3 tools' }));
    expect(await screen.findByText('read_structure')).toBeInTheDocument();
    expect(screen.getByText('https://mcp.deepwiki.com/mcp')).toBeInTheDocument();
  });

  it('renames with a display name and clears it back to the default name', async () => {
    respond([own]);
    render(<I18nProvider><PersonalConnectorSettings viewerId="me" /></I18nProvider>);
    fireEvent.click(within(await openMenu()).getByText('Rename…'));
    const input = await screen.findByLabelText('Display name');
    expect(input).toHaveAttribute('placeholder', 'DeepWiki');
    fireEvent.change(input, { target: { value: ' Wiki docs ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/application-policies', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ ...own, displayName: 'Wiki docs' }) })));
  });

  it('focuses the close button when the rename dialog opens', async () => {
    respond([own]);
    render(<I18nProvider><PersonalConnectorSettings viewerId="me" /></I18nProvider>);
    fireEvent.click(within(await openMenu()).getByText('Rename…'));
    const input = await screen.findByLabelText('Display name');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Close' })).toHaveFocus());
    expect(input).not.toHaveFocus();
  });

  it('opens the tool list on hover', async () => {
    respond([own]);
    render(<I18nProvider><PersonalConnectorSettings viewerId="me" /></I18nProvider>);
    fireEvent.pointerEnter(await screen.findByRole('button', { name: '3 tools' }));
    expect(await screen.findByText('ask_question')).toBeInTheDocument();
  });

  it('reconnects and reports the result', async () => {
    respond([own]);
    render(<I18nProvider><PersonalConnectorSettings viewerId="me" /></I18nProvider>);
    fireEvent.click(within(await openMenu()).getByText('Reconnect'));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('MCP connector reconnected', expect.objectContaining({ id: 'pending' })));
    expect(toast.loading).toHaveBeenCalledWith('Connecting to MCP server…');
    expect(request).toHaveBeenCalledWith('/api/application-policies/wiki/connect', expect.objectContaining({ method: 'POST' }));
  });

  it('starts authorization when the server asks for it on reconnect', async () => {
    respond([own], { connected: false, authorizationRequired: true, error: 'MCP server requires authorization' });
    const assign = vi.fn();
    vi.stubGlobal('location', { ...window.location, assign, search: '', pathname: '/', hash: '' });
    render(<I18nProvider><PersonalConnectorSettings viewerId="me" /></I18nProvider>);
    request.mockImplementationOnce(async () => [own]);
    fireEvent.click(within(await openMenu()).getByText('Reconnect'));
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/application-policies/wiki/oauth/start', expect.objectContaining({ method: 'POST' })));
    vi.unstubAllGlobals();
  });

  it('toggles asking before running tools from the menu', async () => {
    respond([own]);
    render(<I18nProvider><PersonalConnectorSettings viewerId="me" /></I18nProvider>);
    fireEvent.click(within(await openMenu()).getByText('Ask before running tools'));
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/application-policies', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ ...own, toolApproval: 'always' }) })));
  });

  it('removes a connector after confirmation', async () => {
    respond([own]);
    render(<I18nProvider><PersonalConnectorSettings viewerId="me" /></I18nProvider>);
    fireEvent.click(within(await openMenu()).getByText('Remove connector'));
    expect(await screen.findByText('Remove DeepWiki?')).toBeInTheDocument();
    expect(screen.getByText('This MCP connector will be removed and its tools will no longer be available to Flow Agent.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/application-policies/wiki', { method: 'DELETE' }));
  });
});
