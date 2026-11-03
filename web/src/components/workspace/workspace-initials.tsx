import { hasWideWorkspaceInitials, workspaceInitials } from '@/lib/workspace-initials';
import { FittedInitials } from '@/components/ui/fitted-initials';

export function WorkspaceInitials({ name, icon, className = '' }: { name: string; icon?: string; className?: string }) {
  const value = icon || workspaceInitials(name);
  return <span className="workspace-initials-slot" data-i18n-ignore><FittedInitials className={`workspace-initials ${className}`} wide={hasWideWorkspaceInitials(value)}>{value}</FittedInitials></span>;
}
