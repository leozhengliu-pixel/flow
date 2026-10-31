import { useEffect, useRef, useState } from "react";
import { Check } from "lucide-react";
import { Command } from 'cmdk';
import { PhoneMenu } from '@/components/ui/phone-menu';
import { usePhoneLayout } from '@/hooks/use-phone-layout';
import { useOptionalTranslate } from '@/i18n/i18n';
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
  const [switching, setSwitching] = useState(false);
  const sequence = useRef<{ key: string; at: number }>({ key: "", at: 0 });
  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      if (isEditable(event.target)) return;
      const key = event.key.toLowerCase(),
        now = Date.now();
      // Log out (⌥⇧Q), the shortcut the workspace menu advertises.
      if (event.altKey && event.shiftKey && !event.metaKey && !event.ctrlKey && key === "q") {
        event.preventDefault();
        sequence.current = { key: "", at: 0 };
        void onLogout();
        return;
      }
      if (
        sequence.current.key === "o" &&
        key === "w" &&
        now - sequence.current.at < 900
      ) {
        event.preventDefault();
        setOpen(true);
        window.setTimeout(
          () =>
            document
              .querySelector<HTMLElement>("[data-workspace-switch-trigger]")
              ?.click(),
          50,
        );
        sequence.current = { key: "", at: 0 };
        return;
      }
      if (
        sequence.current.key === "g" &&
        key === "s" &&
        now - sequence.current.at < 900
      ) {
        event.preventDefault();
        onSettings("workspace");
        sequence.current = { key: "", at: 0 };
        return;
      }
      if (key === "o" || key === "g") sequence.current = { key, at: now };
    };
    addEventListener("keydown", listener);
    return () => removeEventListener("keydown", listener);
  }, [onLogout, onSettings]);
  const workspace = data.workspace;
  const choose = (run: () => void) => { setOpen(false); run() };
  if (phone) return <>
    <button className="workspace-menu-trigger" aria-label={`${workspace.name} ${t('Workspace Menu')}`} aria-haspopup="dialog" aria-expanded={open} data-state={open ? 'open' : 'closed'} onClick={() => { setSwitching(false); setOpen(true) }}>
      <WorkspaceAvatar workspace={workspace}/><strong>{workspace.name}</strong><WorkspaceChevronIcon phone/>
    </button>
    <PhoneMenu className="workspace-phone-menu" open={open} onOpenChange={setOpen} label={t(switching ? 'Switch workspace' : 'Workspace Menu')}>
      <Command key={switching ? 'switch' : 'workspace'} label={t(switching ? 'Switch workspace' : 'Workspace Menu')}>
        <div className="phone-menu-search"><div className="workspace-phone-menu-input"><Command.Input aria-label={t('Command menu')} placeholder={t('Filter...')}/></div></div>
        <Command.List>
          {switching ? <>
            <Command.Group heading={account.viewer.email}>
              {account.workspaces.map(({ workspace: item }) => <Command.Item key={item.id} value={item.id} keywords={[item.name]} onSelect={() => choose(() => onSwitch(item))}>
                <WorkspaceAvatar workspace={item}/><span data-i18n-ignore>{item.name}</span>{item.id === workspace.id && <Check className="phone-menu-current"/>}
              </Command.Item>)}
            </Command.Group>
            <Command.Separator><span/></Command.Separator>
            <Command.Item onSelect={() => choose(onCreate)}>{t('Create or join a workspace…')}</Command.Item>
          </> : <>
            <WorkspacePhoneItem onSelect={() => choose(() => onSettings('workspace'))}>{t('Settings')}</WorkspacePhoneItem>
            {(data.viewerRole === 'admin' || data.viewerRole === 'owner') && <WorkspacePhoneItem onSelect={() => choose(() => onSettings('members'))}>{t('Invite and manage members')}</WorkspacePhoneItem>}
            <WorkspacePhoneItem onSelect={() => choose(() => { window.open('https://flow.app/download', '_blank') })}>{t('Download desktop app')}</WorkspacePhoneItem>
            <Command.Separator><span/></Command.Separator>
            <WorkspacePhoneItem onSelect={() => setSwitching(true)}>{t('Switch workspace')}</WorkspacePhoneItem>
            <Command.Separator><span/></Command.Separator>
            <WorkspacePhoneItem onSelect={() => choose(() => { void onLogout() })}>{t('Log out')}</WorkspacePhoneItem>
          </>}
        </Command.List>
      </Command>
    </PhoneMenu>
  </>;
  return (
    <>
      <DropdownMenu open={open} onOpenChange={setOpen}>
        <DropdownMenuTrigger
          className="workspace-menu-trigger"
          aria-label={`${workspace.name} Workspace Menu`}
        >
          <WorkspaceAvatar workspace={workspace} />
          <strong>{workspace.name}</strong>
          <WorkspaceChevronIcon />
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="start"
          sideOffset={4}
          className="workspace-primary-menu"
        >
          <div className="workspace-menu-list">
            <DropdownMenuItem onSelect={() => onSettings("workspace")}>
              Settings<WorkspaceMenuShortcut>G then S</WorkspaceMenuShortcut>
            </DropdownMenuItem>
            {/* Settings › Members is an administration page: the reference app only lists it for admins. */}
            {(data.viewerRole === "admin" || data.viewerRole === "owner") && (
              <DropdownMenuItem onSelect={() => onSettings("members")}>
                Invite and manage members
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onSelect={() =>
                window.open("https://flow.app/download", "_blank")
              }
            >
              Download desktop app
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuSub>
              <DropdownMenuSubTrigger data-workspace-switch-trigger>
                Switch workspace
                <WorkspaceMenuShortcut>O then W</WorkspaceMenuShortcut>
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
                    <span className="workspace-option-name">
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
                <DropdownMenuLabel>Account</DropdownMenuLabel>
                <DropdownMenuItem onSelect={onCreate}>
                  Create or join a workspace…
                </DropdownMenuItem>
              </DropdownMenuSubContent>
            </DropdownMenuSub>
            <DropdownMenuItem onSelect={() => void onLogout()}>
              Log out<WorkspaceMenuShortcut>⌥ ⇧ Q</WorkspaceMenuShortcut>
            </DropdownMenuItem>
          </div>
        </DropdownMenuContent>
      </DropdownMenu>
    </>
  );
}

export function WorkspaceAvatar({ workspace }: { workspace: Workspace }) {
  return (
    <span
      className="workspace-avatar"
      style={{ background: workspace.color || "#b89b38" }}
    >
      {workspace.logoUrl ? (
        <img alt="" src={workspace.logoUrl} />
      ) : (
        workspace.icon || initials(workspace.name)
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
function initials(value: string) {
  const parts = value.split(/\s+/).filter(Boolean);
  const result =
    parts.length > 1
      ? parts.map((part) => part[0]).join("")
      : (parts[0] ?? "").slice(0, 2);
  return result.slice(0, 2).toUpperCase() || "W";
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
