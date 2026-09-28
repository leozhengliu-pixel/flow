import * as Select from "@radix-ui/react-select";
import { useEffect, useId, useMemo, useState } from "react";
import { toast } from "sonner";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { SelectControl } from "@/components/ui/select-control";
import { TeamIcon } from "@/components/issue/issue-icons";
import { WorkspaceAvatar } from "@/components/workspace/workspace-menu";
import { useI18n } from "@/i18n/i18n";
import { inviteMembers } from "@/lib/api";
import type { Invitation, Team, Workspace } from "@/types/flow";

import "./invite-members-dialog.css";

export type InviteRole = "guest" | "member" | "admin";

/** Linear's role options, in Linear's order. */
const INVITE_ROLE_OPTIONS: { value: InviteRole; label: string; description: string }[] = [
  { value: "guest", label: "Guest", description: "Limited access to teams" },
  { value: "member", label: "Member", description: "Full access with limited permissions" },
  { value: "admin", label: "Admin", description: "Full administrative access" },
];

const EMAIL_PATTERN = /^[^@\s]+@[^@\s]+\.[^@\s]+$/;

/** Splits the free-form email field into candidate addresses. */
function parseInviteEmails(value: string) {
  const entries = value.split(/[\s,;]+/).map((entry) => entry.trim()).filter(Boolean);
  return {
    valid: [...new Set(entries.filter((entry) => EMAIL_PATTERN.test(entry)))],
    invalid: entries.filter((entry) => !EMAIL_PATTERN.test(entry)),
  };
}

export interface InviteMembersDialogProps {
  workspace: Workspace;
  /** Teams a guest can be scoped to. Guests require at least one team. */
  teams: Team[];
  open: boolean;
  onClose: () => void;
  /** Runs after the invitations were created (e.g. to reload the member list). */
  onInvited?: (invitations: Invitation[]) => void | Promise<void>;
  defaultRole?: InviteRole;
}

export function InviteMembersDialog({ workspace, teams, open, onClose, onInvited, defaultRole = "member" }: InviteMembersDialogProps) {
  const { t } = useI18n();
  const emailId = useId();
  const roleId = useId();
  const [emails, setEmails] = useState("");
  const [error, setError] = useState("");
  const [role, setRole] = useState<InviteRole>(defaultRole);
  const activeTeams = useMemo(() => teams.filter((team) => !team.retiredAt), [teams]);
  const [teamId, setTeamId] = useState("");
  const [sending, setSending] = useState(false);
  const parsed = useMemo(() => parseInviteEmails(emails), [emails]);
  const canSend = parsed.valid.length > 0 && !sending;

  useEffect(() => {
    if (open) return;
    setError("");
    setRole(defaultRole);
  }, [defaultRole, open]);

  const send = async () => {
    if (!canSend) return;
    if (parsed.invalid.length) {
      setError(`${parsed.invalid[0]} ${t("is not a valid email address")}`);
      return;
    }
    const guestTeamId = teamId || activeTeams[0]?.id || "";
    if (role === "guest" && !guestTeamId) {
      setError(t("Select a team for guest access"));
      return;
    }
    setSending(true);
    try {
      const invitations = await inviteMembers(workspace.urlKey, {
        emails: parsed.valid,
        role,
        teamIds: role === "guest" ? [guestTeamId] : [],
      });
      const token = invitations.find((item) => item.token)?.token;
      if (token) await navigator.clipboard?.writeText(`${location.origin}/invite/${token}`).catch(() => undefined);
      await onInvited?.(invitations);
      toast.success(`${parsed.valid.length} invitation${parsed.valid.length === 1 ? "" : "s"} sent`);
      setEmails("");
      onClose();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t("Could not send invitations"));
    } finally {
      setSending(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) onClose(); }}>
      <DialogContent
        className="invite-members-dialog"
        closeLabel="Close modal dialog"
        aria-describedby={undefined}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          document.getElementById(emailId)?.focus();
        }}
      >
        <header className="invite-members-dialog__header">
          <WorkspaceAvatar workspace={workspace} />
          <DialogTitle>{t("Invite to your workspace")}</DialogTitle>
        </header>
        <div className="invite-members-dialog__field is-email">
          <label htmlFor={emailId}>{t("Email")}</label>
          <textarea
            id={emailId}
            autoFocus
            aria-invalid={Boolean(error) || undefined}
            placeholder="email@gmail.com, email2@gmail.com…"
            value={emails}
            onChange={(event) => {
              setEmails(event.target.value);
              setError("");
            }}
            onKeyDown={(event) => {
              if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
                event.preventDefault();
                void send();
              }
            }}
          />
          {error && <p className="invite-members-dialog__error" role="alert">{error}</p>}
        </div>
        <div className="invite-members-dialog__field is-role">
          <label id={roleId}>{t("Role")}</label>
          <InviteRoleSelect labelledBy={roleId} value={role} onChange={setRole} />
        </div>
        {role === "guest" && (
          <div className="invite-members-dialog__field is-team">
            <label>{t("Team")}
              <SelectControl
                label="Team"
                value={teamId || activeTeams[0]?.id || ""}
                onChange={setTeamId}
                options={activeTeams.map((team) => ({ value: team.id, label: team.name, entityName: true, icon: <TeamIcon team={team} size={14} /> }))}
              />
            </label>
          </div>
        )}
        <footer className="invite-members-dialog__footer">
          <button type="button" className="invite-members-dialog__send" disabled={!canSend} onClick={() => void send()}>
            {sending ? t("Sending…") : t("Send invites")}
          </button>
        </footer>
      </DialogContent>
    </Dialog>
  );
}

function InviteRoleSelect({ labelledBy, value, onChange }: { labelledBy: string; value: InviteRole; onChange: (role: InviteRole) => void }) {
  const { t } = useI18n();
  return (
    <Select.Root value={value} onValueChange={(next) => onChange(next as InviteRole)}>
      <Select.Trigger className="invite-members-role-trigger" aria-labelledby={labelledBy}>
        <Select.Value />
        <Select.Icon className="invite-members-role-trigger__chevron">
          <svg aria-hidden="true" viewBox="0 0 10 5" fill="currentColor"><path d="M.2.2a.7.7 0 0 1 1 0L5 3.4 8.8.2a.7.7 0 1 1 .9 1.1L5.5 4.8a.7.7 0 0 1-1 0L.3 1.3A.7.7 0 0 1 .2.2Z" /></svg>
        </Select.Icon>
      </Select.Trigger>
      <Select.Portal>
        <Select.Content data-flow-motion="floating" className="invite-members-role-menu" position="item-aligned">
          <Select.Viewport>
            {INVITE_ROLE_OPTIONS.map((option) => (
              <Select.Item key={option.value} value={option.value} className="invite-members-role-option">
                <Select.ItemText>
                  <span className="invite-members-role__name">{t(option.label)}</span>
                  <span className="invite-members-role__description"> - {t(option.description)}</span>
                </Select.ItemText>
                <Select.ItemIndicator className="invite-members-role-option__check">
                  <svg aria-hidden="true" viewBox="0 0 16 16" fill="currentColor"><path d="M13.53 4.47a.75.75 0 0 1 0 1.06l-6.5 6.5a.75.75 0 0 1-1.06 0l-3.5-3.5a.75.75 0 1 1 1.06-1.06L6.5 10.44l5.97-5.97a.75.75 0 0 1 1.06 0Z" /></svg>
                </Select.ItemIndicator>
              </Select.Item>
            ))}
          </Select.Viewport>
        </Select.Content>
      </Select.Portal>
    </Select.Root>
  );
}
