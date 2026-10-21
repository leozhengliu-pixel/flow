import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApplicationPolicySettings, DataPrivacyDialog } from './application-policy-settings';
import type { WorkspaceSettings } from '@/types/flow';

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock('@/lib/api-client', () => ({ request, jsonRequest: (method: string, body: unknown) => ({ method, body: JSON.stringify(body) }) }));
vi.mock('@/lib/toast', () => ({ toast: { error: vi.fn() } }));

describe('application settings behavior', () => {
  beforeEach(() => { vi.clearAllMocks(); request.mockResolvedValue([]); });
  it('sends a scope-bound administrator decision for a pending application', async () => {
    const policy = { id: 'client', name: 'Review assistant', kind: 'oauth', status: 'pending', shared: true, scopes: ['read'] };
    request.mockResolvedValue([policy]);
    render(<ApplicationPolicySettings admin />);
    fireEvent.click(await screen.findByRole('button', { name: 'Approve' }));
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/application-policies', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ ...policy, status: 'approved' }) })));
  });
  it('does not let a member approve or share a personal connector', async () => {
    request.mockResolvedValue([{ id: 'mcp', name: 'Personal connector', kind: 'mcp', status: 'pending', shared: false, url: 'https://example.test/mcp' }]);
    render(<ApplicationPolicySettings admin={false} />);
    await screen.findByText('Personal connector');
    expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Add connector' }));
    fireEvent.click(await screen.findByRole('option', {name:'Custom URL…'}));
    fireEvent.click(screen.getByRole('button', {name:'Advanced…'}));
    expect(screen.queryByLabelText('Workspace connector')).not.toBeInTheDocument();
    // A member names nothing here: the connector takes its host name until the server reports one.
    expect(screen.queryByLabelText('Name')).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Connector URL'), { target: { value: 'https://example.test/mcp' } });
    fireEvent.submit(screen.getByLabelText('Connector URL').closest('form')!);
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/application-policies', expect.objectContaining({ body: expect.stringContaining('"status":"pending"') })));
  });
  it('names the validation error under the URL field instead of a native bubble', async () => {
    render(<ApplicationPolicySettings admin={false} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Add connector' }));
    fireEvent.click(await screen.findByRole('option', { name: 'Custom URL…' }));
    const submit = screen.getByRole('button', { name: 'Add connector', hidden: false });
    expect(submit).toBeEnabled();
    fireEvent.click(submit);
    expect(await screen.findByRole('alert')).toHaveTextContent('Please enter a valid URL starting with http:// or https://.');
    fireEvent.change(screen.getByLabelText('Connector URL'), { target: { value: 'notaurl' } });
    fireEvent.submit(screen.getByLabelText('Connector URL').closest('form')!);
    expect(screen.getByRole('alert')).toHaveTextContent('valid URL');
    expect(request).not.toHaveBeenCalledWith('/api/application-policies', expect.objectContaining({ method: 'PUT' }));
  });
  it('lets an administrator expose an approved workspace connector to coding sessions', async () => {
    const policy = { id: 'mcp-sourcebot-production', name: 'Sourcebot', kind: 'mcp', status: 'approved', shared: true, url: 'https://hsb.example.net/api/mcp' };
    request.mockResolvedValue([policy]);
    render(<ApplicationPolicySettings admin />);
    fireEvent.click(await screen.findByRole('button', { name: 'Enable for coding sessions' }));
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/application-policies', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ ...policy, codingSessions: true }) })));
  });
  it('offers coding sessions only for approved workspace connectors and only to administrators', async () => {
    request.mockResolvedValue([{ id: 'p', name: 'Pending', kind: 'mcp', status: 'pending', shared: true, url: 'https://example.test/mcp' }, { id: 'a', name: 'Approved', kind: 'mcp', status: 'approved', shared: true, codingSessions: true, url: 'https://example.test/mcp' }]);
    const { unmount } = render(<ApplicationPolicySettings admin />);
    expect(await screen.findByRole('button', { name: 'Disable for coding sessions' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Enable for coding sessions' })).not.toBeInTheDocument();
    unmount();
    render(<ApplicationPolicySettings admin={false} />);
    await screen.findByText('Approved');
    expect(screen.queryByRole('button', { name: /coding sessions/ })).not.toBeInTheDocument();
  });
  it('lets a connector owner switch between running tools automatically and asking first', async () => {
    const policy = { id: 'mcp', name: 'Personal connector', kind: 'mcp', status: 'approved', shared: false, url: 'https://example.test/mcp' };
    request.mockResolvedValue([policy]);
    const { unmount } = render(<ApplicationPolicySettings admin={false} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Ask before running tools' }));
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/application-policies', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ ...policy, toolApproval: 'always' }) })));
    unmount();
    request.mockResolvedValue([{ ...policy, toolApproval: 'always' }]);
    render(<ApplicationPolicySettings admin={false} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Run tools automatically' }));
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/application-policies', expect.objectContaining({ method: 'PUT', body: JSON.stringify(policy) })));
  });
  it('does not offer the tool approval setting of a workspace connector to members', async () => {
    request.mockResolvedValue([{ id: 'w', name: 'Workspace tools', kind: 'mcp', status: 'approved', shared: true, url: 'https://example.test/mcp' }]);
    render(<ApplicationPolicySettings admin={false} />);
    await screen.findByText('Workspace tools');
    expect(screen.queryByRole('button', { name: 'Ask before running tools' })).not.toBeInTheDocument();
  });
  it('keeps the privacy editor open when persistence fails', async () => {
    const save = vi.fn().mockRejectedValue(new Error('Save failed'));
    const close = vi.fn();
    render(<DataPrivacyDialog kind="support" settings={{ reduceSupportPersonalInfo: false } as WorkspaceSettings} onSave={save} onClose={close} />);
    fireEvent.click(screen.getByRole('checkbox'));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(save).toHaveBeenCalledWith({ reduceSupportPersonalInfo: true }));
    expect(close).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save' })).not.toBeDisabled());
  });
});
