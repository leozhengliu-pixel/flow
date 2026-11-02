import { useState } from 'react';
import { useI18n } from '@/i18n/i18n';
import { updateWorkspacePreferences } from '@/lib/api';
import { mcpConnectorsEnabled } from '@/lib/mcp-connectors';
import { toast } from '@/lib/toast';
import type { BootstrapData, WorkspaceSettings } from '@/types/flow';
import { ApplicationPolicySettings } from './application-policy-settings';
import { SettingsPageTitle, SettingsRow, SettingsSection, SettingsSelect, SettingsToggle } from './settings-primitives';

export function McpSettingsPage({ data, onReload }: { data: BootstrapData; onReload: () => Promise<void> }) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const settings = data.workspaceSettings;
  const save = async (next: Partial<WorkspaceSettings>) => {
    setBusy(true);
    try { await updateWorkspacePreferences(next); await onReload(); }
    catch (error) { toast.error(error instanceof Error ? error.message : 'Could not update feature settings'); }
    finally { setBusy(false); }
  };
  return <>
    <SettingsPageTitle description={t('Manage connector access and connections for your workspace')}>{t('MCP connectors')}</SettingsPageTitle>
    <SettingsSection>
      <SettingsRow title={t('Enable connectors')} description={t('Allow connectors to be used in this workspace')}>
        <SettingsToggle label={t('Enable connectors')} checked={mcpConnectorsEnabled(settings)} disabled={busy || Boolean(settings.hipaaCompliance)} onChange={value => void save({ mcpConnectors: value })}/>
      </SettingsRow>
      <SettingsRow className="settings-row-select" title={t('Manage connectors')} description={t('Choose which connectors are allowed in this workspace')}>
        <SettingsSelect label={t('Manage connectors')} value={(settings.allowedMcpConnectors ?? 'all') === 'all' ? 'All connectors' : 'Approved connectors'} options={['All connectors', 'Approved connectors']} disabled={busy} onChange={value => void save({ allowedMcpConnectors: value === 'All connectors' ? 'all' : 'approved' })}/>
      </SettingsRow>
    </SettingsSection>
    <ApplicationPolicySettings admin title="Connectors"/>
  </>;
}
