import { hasWideWorkspaceInitials, workspaceInitials } from '@/lib/workspace-initials';

export function WorkspaceInitials({ name, icon, className = '' }: { name: string; icon?: string; className?: string }) {
  const value = icon || workspaceInitials(name);
  return <span className={`workspace-initials ${className}`} data-i18n-ignore data-wide={hasWideWorkspaceInitials(value) ? 'true' : undefined}>{value}</span>;
}
