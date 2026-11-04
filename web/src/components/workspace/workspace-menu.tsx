import { useEffect, useRef, useState, type FocusEvent } from "react";
import { Check } from "lucide-react";
import { Command } from 'cmdk';
import { PhoneMenu, PhoneMenuCommand } from '@/components/ui/phone-menu';
import { usePhoneLayout } from '@/hooks/use-phone-layout';
import { useOptionalTranslate } from '@/i18n/i18n';
import { useOnlineStatus } from '@/hooks/use-online-status';
import { OfflineIndicator } from './offline-indicator';
import { WorkspaceInitials } from './workspace-initials';
import type { AccountBootstrap, BootstrapData, Workspace } from "@/types/flow";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

export function WorkspaceMenu({
  account,
  data,
  onSettings,
  onSwitch,
  onCreate,
  onLogout,
}: {
  account: AccountBootstrap;
  data: BootstrapData;
  onSwitch: (workspace: Workspace) => void;
  onCreate: () => void;
  onSettings: (page?: "workspace" | "members") => void;
  onLogout: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const phone = usePhoneLayout();
  const t = useOptionalTranslate();
  const online = useOnlineStatus();
  const [switching, setSwitching] = useState(false);
  const switchFrame = useRef<number | undefined>(undefined);
  const sequence = useRef<{ key: string; at: number }>({ key: "", at: 0 });
  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      if (isEditable(event.target)) return;
      const key = event.key.toLowerCase(),
        now = Date.now();
      // Log out (⌥⇧Q), the shortcut the workspace menu advertises.
      if (event.altKey && event.shiftKey && !event.metaKey && !event.ctrlKey && (key === "q" || event.code === "KeyQ")) {
        event.preventDefault();
        sequence.current = { key: "", at: 0 };
        void onLogout();
        return;
      }
      if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey || event.repeat) {
        sequence.current = { key: "", at: 0 };
        return;
      }
      if (
        sequence.current.key === "o" &&
        key === "w" &&
        now - sequence.current.at < 900
      ) {
        event.preventDefault();
        setOpen(true);
        if (phone) setSwitching(true);
        else {
          window.cancelAnimationFrame(switchFrame.current ?? 0);
          switchFrame.current = window.requestAnimationFrame(() => {
            document.querySelector<HTMLElement>('[data-workspace-switch-trigger]')?.focus();
            setSwitching(true);
          });
        }
        sequence.current = { key: "", at: 0 };
        return;
      }
      if (
        sequence.current.key === "g" &&
        key === "s" &&
        now - sequence.current.at < 900
      ) {
        event.preventDefault();
        setOpen(false);
        setSwitching(false);
        onSettings("workspace");
        sequence.current = { key: "", at: 0 };
        return;
      }
      sequence.current = key === "o" || key === "g" ? { key, at: now } : { key: "", at: 0 };
    };
    addEventListener("keydown", listener);
    return () => {
      removeEventListener("keydown", listener);
      window.cancelAnimationFrame(switchFrame.current ?? 0);
    };
  }, [onLogout, onSettings, phone]);
  const workspace = data.workspace;
  const choose = (run: () => void) => { setOpen(false); run() };
  if (phone) return <>
    <button className="workspace-menu-trigger" aria-label={`${workspace.name} ${t('Workspace Menu')}`} aria-haspopup="dialog" aria-expanded={open} data-state={open ? 'open' : 'closed'} onClick={() => { setSwitching(false); setOpen(true) }}>
      <WorkspaceAvatar workspace={workspace}/><strong data-i18n-ignore>{workspace.name}</strong><WorkspaceChevronIcon phone/>
    </button>
    <PhoneMenu className="workspace-phone-menu" open={open} onOpenChange={setOpen} label={t(switching ? 'Switch workspace' : 'Workspace Menu')}>
      <PhoneMenuCommand key={switching ? 'switch' : 'workspace'} label={t(switching ? 'Switch workspace' : 'Workspace Menu')}>
        <div className="phone-menu-search"><div className="workspace-phone-menu-input"><Command.Input aria-label={t('Command menu')} placeholder={t('Filter...')}/></div></div>
        <Command.List>
          {switching ? <>
            <Command.Group heading={account.viewer.email}>
              {account.workspaces.map(({ workspace: item }) => <Command.Item key={item.id} value={item.id} keywords={[item.name]} onSelect={() => choose(() => onSwitch(item))}>
                <WorkspaceAvatar workspace={item}/><span className="workspace-option-name" data-i18n-ignore>{item.name}</span>{item.id === workspace.id && <Check className="phone-menu-current"/>}
              </Command.Item>)}
            </Command.Group>
            <Command.Separator><span/></Command.Separator>
            <Command.Item onSelect={() => choose(onCreate)}>{t('Create or join a workspace…')}</Command.Item>
          </> : <>
            <WorkspacePhoneItem onSelect={() => choose(() => onSettings('workspace'))}>{t('Settings')}</WorkspacePhoneItem>
            {(data.viewerRole === 'admin' || data.viewerRole === 'owner') && <WorkspacePhoneItem onSelect={() => choose(() => onSettings('members'))}>{t('Invite and manage members')}</WorkspacePhoneItem>}
            <Command.Separator><span/></Command.Separator>
            <WorkspacePhoneItem onSelect={() => setSwitching(true)}>{t('Switch workspace')}</WorkspacePhoneItem>
            <Command.Separator><span/></Command.Separator>
            <WorkspacePhoneItem onSelect={() => choose(() => { void onLogout() })}>{t('Log out')}</WorkspacePhoneItem>
          </>}
        </Command.List>
      </PhoneMenuCommand>
    </PhoneMenu>
  </>;
  const closeCascadeFromSibling = (event: FocusEvent<HTMLDivElement>) => {
    if (event.relatedTarget instanceof Element && event.relatedTarget.closest('.workspace-primary-menu,.workspace-switch-menu')) setSwitching(false)
  };
  return (
    <>
      <DropdownMenu open={open} onOpenChange={next => { setOpen(next); if (!next) { window.cancelAnimationFrame(switchFrame.current ?? 0); setSwitching(false) } }}>
        <DropdownMenuTrigger
          className="workspace-menu-trigger"
          aria-label={`${workspace.name} ${t('Workspace Menu')}`}
        >
          <WorkspaceAvatar workspace={workspace} />
          {online && <strong data-i18n-ignore>{workspace.name}</strong>}
          <WorkspaceChevronIcon />
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="start"
          sideOffset={4}
          className="workspace-primary-menu"
          onPointerMove={event => {
            const item = (event.target as Element).closest<HTMLElement>('[role="menuitem"]')
            if (switching && item && !item.hasAttribute('data-workspace-switch-trigger') && !item.hasAttribute('data-disabled')) { setSwitching(false); item.focus({ preventScroll: true }) }
          }}
        >
          <div className="workspace-menu-list">
            <DropdownMenuItem onFocus={closeCascadeFromSibling} onSelect={() => onSettings("workspace")}>
              {t('Settings')}<WorkspaceMenuShortcut>{t('G then S')}</WorkspaceMenuShortcut>
            </DropdownMenuItem>
            {/* Settings › Members is an administration page: the reference app only lists it for admins. */}
            {(data.viewerRole === "admin" || data.viewerRole === "owner") && (
              <DropdownMenuItem onFocus={closeCascadeFromSibling} onSelect={() => onSettings("members")}>
                {t('Invite and manage members')}
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuSub open={switching} onOpenChange={setSwitching}>
              <DropdownMenuSubTrigger data-workspace-switch-trigger className="workspace-switch-trigger">
                {t('Switch workspace')}
                <WorkspaceMenuShortcut>{t('O then W')}</WorkspaceMenuShortcut>
                <span className="workspace-switch-arrow" aria-hidden="true">▶</span>
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent
                sideOffset={5}
                alignOffset={-5}
                className="workspace-switch-menu"
              >
                <DropdownMenuLabel>{account.viewer.email}</DropdownMenuLabel>
                {account.workspaces.map((membership) => (
                  <DropdownMenuItem
                    key={membership.workspace.id}
                    onSelect={() => onSwitch(membership.workspace)}
                  >
                    <WorkspaceAvatar workspace={membership.workspace} />
                    <span className="workspace-option-name" data-i18n-ignore>
                      {membership.workspace.name}
                    </span>
                    {membership.workspace.id === workspace.id && (
                      <Check size={14} />
                    )}
                    <span className="workspace-count">
                      {membership.issueCount || ""}
                    </span>
                  </DropdownMenuItem>
                ))}
                <DropdownMenuLabel>{t('Account')}</DropdownMenuLabel>
                <DropdownMenuItem onSelect={onCreate}>
                  {t('Create or join a workspace…')}
                </DropdownMenuItem>
              </DropdownMenuSubContent>
            </DropdownMenuSub>
            <DropdownMenuItem onFocus={closeCascadeFromSibling} onSelect={() => void onLogout()}>
              {t('Log out')}<WorkspaceMenuShortcut>⌥ ⇧ Q</WorkspaceMenuShortcut>
            </DropdownMenuItem>
          </div>
        </DropdownMenuContent>
      </DropdownMenu>
      {!online && <OfflineIndicator />}
    </>
  );
}

export function WorkspaceAvatar({ workspace }: { workspace: Workspace }) {
  return (
    <span
      className="workspace-avatar"
      aria-hidden="true"
      style={{ background: workspace.color || "#b89b38" }}
    >
      {workspace.logoUrl ? (
        <img alt="" src={workspace.logoUrl} />
      ) : (
        <WorkspaceInitials name={workspace.name} icon={workspace.icon} />
      )}
    </span>
  );
}
function WorkspacePhoneItem({ children, onSelect }: { children: string; onSelect: () => void }) {
  return <Command.Group className="workspace-phone-menu-row"><Command.Item onSelect={onSelect}><span>{children}</span></Command.Item></Command.Group>;
}

function WorkspaceMenuShortcut({ children }: { children: React.ReactNode }) {
  return <kbd className="workspace-shortcut">{children}</kbd>;
}
function WorkspaceChevronIcon({ phone = false }: { phone?: boolean }) {
  return (
    <svg viewBox="0 0 13 9" aria-hidden="true">
      <path d={phone ? "M10.1611 0.314094L5.99463 4.48054L1.82819 0.314094C1.4094 -0.104698 0.732886 -0.104698 0.314094 0.314094C-0.104698 0.732886 -0.104698 1.4094 0.314094 1.82819L5.24295 6.75705C5.66175 7.17584 6.33825 7.17584 6.75705 6.75705L11.6859 1.82819C12.1047 1.4094 12.1047 0.732886 11.6859 0.314094C11.2671 -0.0939598 10.5799 -0.104698 10.1611 0.314094Z" : "M10.1611 .314 5.9946 4.4805 1.8282 .314A1.0707 1.0707 0 0 0 .314 1.8282l4.929 4.9289a1.0707 1.0707 0 0 0 1.5141 0L11.686 1.8282A1.0707 1.0707 0 0 0 10.1611 .314Z"} />
    </svg>
  );
}
function isEditable(target: EventTarget | null) {
  return (
    target instanceof HTMLElement &&
    (target.isContentEditable ||
      ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName))
  );
}
