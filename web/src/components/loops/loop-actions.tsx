import type { ReactNode } from "react";
import { ArrowRightLeft, Copy, GitCommitVertical, History, Link2, Pencil, Play, Power, PowerOff, Star, Trash2, UserRound } from "lucide-react";
import { toast } from "sonner";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
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
import type { BootstrapData, Loop } from "@/types/flow";
import { isLoopDraft, loopTeamId } from "./loop-model";
import { activeTeams, copyText, loopUrl } from "./loop-data";

/** Linear's loop menu (⋯ and right-click): Run · Disable │ Edit · Duplicate · Move ▸ · Change owner ▸ │ Favorite · Copy ▸ │ Show run history · Show published versions │ Delete. */
export function LoopActionsMenu({
  data,
  loop,
  trigger,
  onNavigate,
  onChanged,
  onDeleted,
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
