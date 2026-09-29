import { useMemo, useState, type ReactNode } from "react";
import { Command } from "cmdk";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { StatusIcon, TeamIcon } from "@/components/issue/issue-icons";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, Issue } from "@/types/flow";
import { activeTeams } from "./loop-data";

export type PickerItem = {
  id: string;
  label: string;
  icon?: ReactNode;
  detail?: string;
  /** Where the muted detail sits: before the label (issue identifiers) or after it (team keys, like Linear). */
  detailPosition?: "before" | "after";
  /** Business names are not translated. */
  entity?: boolean;
  disabled?: boolean;
  hint?: string;
  keywords?: string;
};

/** Command-style picker dialog shared by "Where should the loop be created?" and "Run loop on…". */
export function LoopCommandPicker({
  open,
  title,
  placeholder,
  items,
  heading,
  footer,
  emptyLabel = "No results found.",
  onSelect,
  onOpenChange,
  onQueryChange,
}: {
  open: boolean;
  title: string;
  placeholder: string;
  items: PickerItem[];
  heading?: string;
  footer?: ReactNode;
  emptyLabel?: string;
  onSelect: (item: PickerItem) => void;
  onOpenChange: (open: boolean) => void;
  onQueryChange?: (query: string) => void;
}) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) setQuery("");
        onOpenChange(next);
      }}
    >
      <DialogContent className="command-dialog loops-picker-dialog" overlayClassName="command-overlay loops-picker-overlay" closeLabel={t("Close")}>
        <DialogTitle className="sr-only">{t(title)}</DialogTitle>
        <Command shouldFilter={!onQueryChange} loop>
          <div className="command-input">
            <Command.Input
              aria-label={t(title)}
              autoFocus
              placeholder={t(placeholder)}
              value={query}
              onValueChange={(value) => {
                setQuery(value);
                onQueryChange?.(value);
              }}
            />
          </div>
          <Command.List>
            <Command.Group heading={heading ? t(heading) : undefined}>
              {items.map((item) => (
                <Command.Item
                  key={item.id}
                  disabled={item.disabled}
                  value={`${item.id} ${item.label} ${item.detail ?? ""} ${item.keywords ?? ""}`}
                  onSelect={() => !item.disabled && onSelect(item)}
                  className={item.disabled ? "loops-picker-item is-disabled" : "loops-picker-item"}
                >
                  {item.icon && <span className="command-item-icon">{item.icon}</span>}
                  <span className="loops-picker-copy">
                    {item.detail && item.detailPosition !== "after" && <small data-i18n-ignore>{item.detail}</small>}
                    <span data-i18n-ignore={item.entity || undefined}>{item.entity ? item.label : t(item.label)}</span>
                    {item.detail && item.detailPosition === "after" && <small data-i18n-ignore>{item.detail}</small>}
                  </span>
                  {item.hint && <small className="loops-picker-hint">{t(item.hint)}</small>}
                </Command.Item>
              ))}
            </Command.Group>
            <Command.Empty>{t(emptyLabel)}</Command.Empty>
          </Command.List>
          {footer && <footer className="loops-picker-footer">{footer}</footer>}
        </Command>
      </DialogContent>
    </Dialog>
  );
}

export type LoopLocation = { level: "workspace" } | { level: "team"; teamId: string };

/** "Where should the loop be created?" — Workspace or a team; team-only templates disable Workspace. */
export function LoopLocationPicker({
  data,
  open,
  requiresTeam = false,
  levelHint,
  onOpenChange,
  onSelect,
}: {
  data: Pick<BootstrapData, "teams" | "workspace">;
  open: boolean;
  requiresTeam?: boolean;
  levelHint?: string;
  onOpenChange: (open: boolean) => void;
  onSelect: (location: LoopLocation) => void;
}) {
  const teams = activeTeams(data.teams);
  const items: PickerItem[] = [
    {
      id: "__workspace__",
      label: "Workspace",
      icon: <ViewGlyph color="currentColor" icon="Team" />,
      disabled: requiresTeam,
      hint: requiresTeam ? (levelHint ?? "Triage loops must belong to a team") : undefined,
    },
    ...teams.map((team) => ({ id: team.id, label: team.name, entity: true, icon: <TeamIcon team={team} size={14} />, detail: team.key, detailPosition: "after" as const, keywords: team.key })),
  ];
  return (
    <LoopCommandPicker
      open={open}
      title="Where should the loop be created?"
      placeholder="Where should the loop be created?"
      items={items}
      onOpenChange={onOpenChange}
      onSelect={(item) => onSelect(item.id === "__workspace__" ? { level: "workspace" } : { level: "team", teamId: item.id })}
    />
  );
}

/** "Search for issue to run loop on…" — recent issues first, filtered by identifier or title. */
export function RunLoopOnPicker({
  data,
  open,
  teamIds,
  onOpenChange,
  onSelect,
}: {
  data: Pick<BootstrapData, "issues">;
  open: boolean;
  /** Limit to the loop's team scope when set. */
  teamIds?: string[];
  onOpenChange: (open: boolean) => void;
  onSelect: (issue: Issue) => void;
}) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const issues = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return data.issues
      .filter((issue) => !teamIds?.length || teamIds.includes(issue.team?.id))
      .filter((issue) => !needle || `${issue.identifier} ${issue.title}`.toLowerCase().includes(needle))
      .sort((a, b) => (b.updatedAt ?? "").localeCompare(a.updatedAt ?? ""))
      .slice(0, needle ? 50 : 12);
  }, [data.issues, query, teamIds]);
  const items: PickerItem[] = issues.map((issue) => {
    const state = issue.state;
    return { id: issue.id, label: issue.title, detail: issue.identifier, entity: true, icon: state ? <StatusIcon state={state} size={14} /> : undefined };
  });
  return (
    <LoopCommandPicker
      open={open}
      title="Search for issue to run loop on…"
      placeholder="Search for issue to run loop on…"
      heading={query.trim() ? undefined : "Recent issues"}
      items={items}
      emptyLabel="No issues found."
      onOpenChange={(next) => {
        if (!next) setQuery("");
        onOpenChange(next);
      }}
      onQueryChange={setQuery}
      onSelect={(item) => {
        const issue = issues.find((candidate) => candidate.id === item.id);
        if (issue) onSelect(issue);
      }}
      footer={
        <span>
          <kbd>↵</kbd> {t("Run loop on")}
        </span>
      }
    />
  );
}
