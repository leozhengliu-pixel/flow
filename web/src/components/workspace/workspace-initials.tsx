import { hasWideWorkspaceInitials, workspaceInitials } from '@/lib/workspace-initials';
import { FittedInitials } from '@/components/ui/fitted-initials';

// The caller's class sits on the outer slot so a consumer that sizes or paints the swatch keeps styling the element
// that is a direct child of its layout; the fitted text lives inside it.
export function WorkspaceInitials({ name, icon, className = '' }: { name: string; icon?: string; className?: string }) {
  const value = icon || workspaceInitials(name);
  return <span className={`workspace-initials-slot ${className}`} data-i18n-ignore><FittedInitials className="workspace-initials" wide={hasWideWorkspaceInitials(value)}>{value}</FittedInitials></span>;
}
