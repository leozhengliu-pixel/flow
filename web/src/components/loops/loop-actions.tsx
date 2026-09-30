import type { ReactNode } from "react";
import { ArrowRightLeft, Check, Copy, GitCommitVertical, History, Link2, Lock, Pencil, Play, Power, PowerOff, SquareUserRound, Star, Trash2, UserRound, UserRoundPen, UserStar } from "lucide-react";
import { toast } from "sonner";
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
import { confirmAction } from "@/components/ui/action-dialog-service";
import { UserAvatar } from "@/components/ui/user-avatar";
import { TeamIcon } from "@/components/issue/issue-icons";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { deleteLoop, duplicateLoop, updateLoop } from "@/lib/api";
import { editLoopPath, loopPath, loopRunPath, loopsPath, newLoopPath } from "@/lib/app-routes";
import { findFavorite, toggleFavoriteFor } from "@/lib/favorites";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, Loop, LoopEditPolicy } from "@/types/flow";
import { LOOP_EDIT_POLICIES, isLoopDraft, loopEditPolicyLabel, loopTeamId } from "./loop-model";
import { activeTeams, copyText, loopUrl } from "./loop-data";

export function LoopEditPolicyIcon({ policy, size = 14 }: { policy: LoopEditPolicy | undefined; size?: number }) {
  if (policy === "owner") return <UserRoundPen size={size} />;
  if (policy === "teamOwners") return <UserStar size={size} />;
  return <SquareUserRound size={size} />;
}

/** "Who can edit this loop" items for the loop menu's submenu and the loop page header. */
export function LoopEditPolicyItems({ current, teamLoop, onSelect }: { current: LoopEditPolicy | undefined; teamLoop: boolean; onSelect: (policy: LoopEditPolicy) => void }) {
  const { t } = useI18n();
  // `undefined` is a mixed selection: nothing is checked.
  const selected = current;
  return (
    <>
      <DropdownMenuLabel>{t("Who can edit this loop")}</DropdownMenuLabel>
      {LOOP_EDIT_POLICIES.map((policy) => (
        <DropdownMenuItem key={policy} onSelect={() => policy !== selected && onSelect(policy)}>
          <LoopEditPolicyIcon policy={policy} />
          {t(loopEditPolicyLabel(policy, teamLoop))}
          {policy === selected && <Check size={14} className="loops-menu-check" aria-label={t("Selected")} />}
        </DropdownMenuItem>
      ))}
    </>
  );
}

/** Linear's loop menu (⋯ and right-click): Run · Disable │ Edit · Duplicate · Move ▸ · Change owner ▸ · Who can edit ▸ │ Favorite · Copy ▸ │ Show run history · Show published versions │ Delete. */
export function LoopActionsMenu({
  data,
  loop,
  trigger,
  onNavigate,
  onChanged,
  onDeleted,
  onDuplicated,
  onRun,
  onShowVersions,
  align = "end",
  open,
  onOpenChange,
}: {
  data: BootstrapData;
  loop: Loop;
  trigger: ReactNode;
  onNavigate: (path: string) => void;
  onChanged: (loop: Loop) => void;
  onDeleted: () => void;
  /** The copy made by Duplicate, added to the loops list before its draft opens. */
  onDuplicated?: (copy: Loop) => void;
  /** Run now for schedules, "Run loop on…" for event loops. */
  onRun?: () => void;
  /** Opens the versions view in place; without it the menu opens the loop page with `?versions=1`. */
  onShowVersions?: () => void;
  align?: "start" | "end";
  /** Controlled open state, for the list's right-click menu. */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  const { t } = useI18n();
  const workspace = data.workspace.urlKey;
  const draft = isLoopDraft(loop);
  const favorite = findFavorite(data.favorites, data.viewer.id, "loop", loop.id);
  const teamId = loopTeamId(loop);
  const patch = async (input: Parameters<typeof updateLoop>[1], success?: string) => {
    try {
      const next = await updateLoop(loop.id, input);
      onChanged(next);
      if (success) toast.success(t(success));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not update loop"));
    }
  };
  const duplicate = async () => {
    try {
      const copy = await duplicateLoop(loop.id);
      onDuplicated?.(copy);
      onNavigate(`${newLoopPath(workspace)}?draftId=${encodeURIComponent(copy.id)}`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not duplicate loop"));
    }
  };
  const remove = async () => {
    const confirmed = await confirmAction(t("Delete loop?"), {
      description: t("This loop and its configuration will be permanently deleted."),
      confirmLabel: t("Delete"),
      danger: true,
    });
    if (!confirmed) return;
    try {
      await deleteLoop(loop.id);
      onDeleted();
      onNavigate(loopsPath(workspace));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not delete loop"));
    }
  };
  const people = data.users.filter((user) => user.active !== false && !user.app);
  return (
    <DropdownMenu open={open} onOpenChange={onOpenChange}>
      <DropdownMenuTrigger asChild>{trigger}</DropdownMenuTrigger>
      <DropdownMenuContent align={align} className="loops-menu loops-actions-menu">
        {!draft && onRun && (
          <DropdownMenuItem disabled={!loop.enabled} onSelect={onRun}>
            <Play size={14} />
            {t(loop.triggerType === "schedule" ? "Run now" : "Run loop on…")}
          </DropdownMenuItem>
        )}
        {!draft && (
          <DropdownMenuItem onSelect={() => void patch({ enabled: !loop.enabled }, loop.enabled ? "Loop disabled" : "Loop enabled")}>
            {loop.enabled ? <PowerOff size={14} /> : <Power size={14} />}
            {t(loop.enabled ? "Disable" : "Enable")}
          </DropdownMenuItem>
        )}
        {draft && (
          <DropdownMenuItem onSelect={() => onNavigate(`${newLoopPath(workspace)}?draftId=${encodeURIComponent(loop.id)}`)}>
            <Play size={14} />
            {t("Continue editing")}
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        {!draft && (
          <DropdownMenuItem onSelect={() => onNavigate(editLoopPath(workspace, loop.id))}>
            <Pencil size={14} />
            {t("Edit")}
          </DropdownMenuItem>
        )}
        <DropdownMenuItem onSelect={() => void duplicate()}>
          <Copy size={14} />
          {t("Duplicate")}
        </DropdownMenuItem>
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            <ArrowRightLeft size={14} />
            {t("Move")}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="loops-menu">
            <DropdownMenuItem disabled={loop.level === "workspace"} onSelect={() => void patch({ level: "workspace", teamId: undefined }, "Loop moved")}>
              <ViewGlyph color="currentColor" icon="Team" />
              {t("Workspace")}
            </DropdownMenuItem>
            {activeTeams(data.teams).map((team) => (
              <DropdownMenuItem key={team.id} disabled={teamId === team.id} onSelect={() => void patch({ level: "team", teamId: team.id }, "Loop moved")}>
                <TeamIcon team={team} size={14} />
                <span data-i18n-ignore>{team.name}</span>
              </DropdownMenuItem>
            ))}
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            <UserRound size={14} />
            {t("Change owner")}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="loops-menu loops-value-menu">
            {people.map((user) => (
              <DropdownMenuItem key={user.id} disabled={(loop.ownerId ?? loop.creator?.id) === user.id} onSelect={() => void patch({ ownerId: user.id }, "Owner changed")}>
                <UserAvatar avatarUrl={user.avatarUrl} className="avatar loops-avatar" name={user.displayName || user.name} />
                <span data-i18n-ignore>{user.displayName || user.name}</span>
              </DropdownMenuItem>
            ))}
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        {!draft && (
          <DropdownMenuSub>
            <DropdownMenuSubTrigger>
              <Lock size={14} />
              {t("Who can edit")}
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent className="loops-menu">
              <LoopEditPolicyItems current={loop.editPolicy ?? "all"} teamLoop={Boolean(teamId)} onSelect={(editPolicy) => void patch({ editPolicy }, "Edit access updated")} />
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        )}
        <DropdownMenuSeparator />
        {!draft && (
          <DropdownMenuItem
            onSelect={() =>
              void toggleFavoriteFor(data, "loop", loop.id).catch((error: unknown) =>
                toast.error(error instanceof Error ? error.message : t("Could not update favorites")),
              )
            }
          >
            <Star size={14} />
            {t(favorite ? "Remove from favorites" : "Favorite")}
            <span className="loops-menu-shortcut">⌥F</span>
          </DropdownMenuItem>
        )}
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            <Link2 size={14} />
            {t("Copy")}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="loops-menu">
            <DropdownMenuItem onSelect={() => void copyText(loopUrl(workspace, loop), t("Link copied"))}>{t("Copy link")}</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => void copyText(loop.id, t("ID copied"))}>{t("Copy ID")}</DropdownMenuItem>
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        {!draft && <DropdownMenuSeparator />}
        {!draft && (
          <DropdownMenuItem onSelect={() => onNavigate(loopRunPath(workspace, loop.id))}>
            <History size={14} />
            {t("Show run history")}
          </DropdownMenuItem>
        )}
        {!draft && (
          <DropdownMenuItem onSelect={() => (onShowVersions ? onShowVersions() : onNavigate(`${loopPath(workspace, loop.id)}?versions=1`))}>
            <GitCommitVertical size={14} />
            {t("Show published versions")}
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => void remove()}>
          <Trash2 size={14} />
          {t("Delete")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** Right-click menu on a multi-row selection: the loop menu's actions that apply to several loops at once. */
export function LoopBulkActionsMenu({
  data,
  loops,
  trigger,
  open,
  onOpenChange,
  onChanged,
  onDeleted,
}: {
  data: BootstrapData;
  loops: Loop[];
  trigger: ReactNode;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onChanged: (loops: Loop[]) => void;
  onDeleted: (ids: string[]) => void;
}) {
  const { t } = useI18n();
  const published = loops.filter((loop) => !isLoopDraft(loop));
  const teamIds = new Set(published.map((loop) => loopTeamId(loop) ?? ""));
  const teamLoops = published.length > 0 && !teamIds.has("");
  const sharedPolicy = new Set(published.map((loop) => loop.editPolicy ?? "all")).size === 1 ? (published[0]?.editPolicy ?? "all") : undefined;
  const count = (template: string, amount: number) => t(template).replace("{count}", String(amount));
  const apply = async (targets: Loop[], input: Parameters<typeof updateLoop>[1], success: string) => {
    const results = await Promise.allSettled(targets.map((loop) => updateLoop(loop.id, input)));
    const changed = results.flatMap((result) => (result.status === "fulfilled" ? [result.value] : []));
    if (changed.length) {
      onChanged(changed);
      toast.success(count(success, changed.length));
    }
    const failed = results.find((result): result is PromiseRejectedResult => result.status === "rejected");
    if (failed) toast.error(failed.reason instanceof Error ? failed.reason.message : t("Could not update loop"));
  };
  const remove = async () => {
    const confirmed = await confirmAction(count("Delete {count} loops?", loops.length), {
      description: t("These loops and their configuration will be permanently deleted."),
      confirmLabel: t("Delete"),
      danger: true,
    });
    if (!confirmed) return;
    const results = await Promise.allSettled(loops.map((loop) => deleteLoop(loop.id).then(() => loop.id)));
    const deleted = results.flatMap((result) => (result.status === "fulfilled" ? [result.value] : []));
    if (deleted.length) onDeleted(deleted);
    const failed = results.find((result): result is PromiseRejectedResult => result.status === "rejected");
    if (failed) toast.error(failed.reason instanceof Error ? failed.reason.message : t("Could not delete loop"));
  };
  const people = data.users.filter((user) => user.active !== false && !user.app);
  const disabled = published.filter((loop) => !loop.enabled);
  const enabled = published.filter((loop) => loop.enabled);
  return (
    <DropdownMenu open={open} onOpenChange={onOpenChange}>
      <DropdownMenuTrigger asChild>{trigger}</DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="loops-menu loops-actions-menu" aria-label={count("{count} loops selected", loops.length)}>
        <DropdownMenuLabel>{count("{count} loops selected", loops.length)}</DropdownMenuLabel>
        {disabled.length > 0 && (
          <DropdownMenuItem onSelect={() => void apply(disabled, { enabled: true }, "{count} loops enabled")}>
            <Power size={14} />
            {t("Enable")}
          </DropdownMenuItem>
        )}
        {enabled.length > 0 && (
          <DropdownMenuItem onSelect={() => void apply(enabled, { enabled: false }, "{count} loops disabled")}>
            <PowerOff size={14} />
            {t("Disable")}
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            <ArrowRightLeft size={14} />
            {t("Move")}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="loops-menu">
            <DropdownMenuItem onSelect={() => void apply(loops, { level: "workspace", teamId: undefined }, "{count} loops moved")}>
              <ViewGlyph color="currentColor" icon="Team" />
              {t("Workspace")}
            </DropdownMenuItem>
            {activeTeams(data.teams).map((team) => (
              <DropdownMenuItem key={team.id} onSelect={() => void apply(loops, { level: "team", teamId: team.id }, "{count} loops moved")}>
                <TeamIcon team={team} size={14} />
                <span data-i18n-ignore>{team.name}</span>
              </DropdownMenuItem>
            ))}
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            <UserRound size={14} />
            {t("Change owner")}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="loops-menu loops-value-menu">
            {people.map((user) => (
              <DropdownMenuItem key={user.id} onSelect={() => void apply(loops, { ownerId: user.id }, "Owner changed for {count} loops")}>
                <UserAvatar avatarUrl={user.avatarUrl} className="avatar loops-avatar" name={user.displayName || user.name} />
                <span data-i18n-ignore>{user.displayName || user.name}</span>
              </DropdownMenuItem>
            ))}
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        {published.length > 0 && (
          <DropdownMenuSub>
            <DropdownMenuSubTrigger>
              <Lock size={14} />
              {t("Who can edit")}
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent className="loops-menu">
              <LoopEditPolicyItems current={sharedPolicy} teamLoop={teamLoops} onSelect={(editPolicy) => void apply(published, { editPolicy }, "Edit access updated for {count} loops")} />
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => void remove()}>
          <Trash2 size={14} />
          {t("Delete")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
