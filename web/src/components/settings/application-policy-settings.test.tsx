import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApplicationPolicySettings, DataPrivacyDialog } from './application-policy-settings';
import type { WorkspaceSettings } from '@/types/flow';

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock('@/lib/api-client', () => ({ request, jsonRequest: (method: string, body: unknown) => ({ method, body: JSON.stringify(body) }) }));
vi.mock('sonner', () => ({ toast: { error: vi.fn() } }));

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
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Tools' } });
    fireEvent.change(screen.getByLabelText('Connector URL'), { target: { value: 'https://example.test/mcp' } });
    fireEvent.submit(screen.getByLabelText('Connector URL').closest('form')!);
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/application-policies', expect.objectContaining({ body: expect.stringContaining('"status":"pending"') })));
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
