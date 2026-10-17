import { ArrowDown, ArrowUp, ListFilter, Search, SlidersHorizontal } from "lucide-react";
import { useMemo, useState } from "react";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { UserAvatar } from "@/components/ui/user-avatar";
import { useI18n } from "@/i18n/i18n";
import { updateWorkspacePreferences } from "@/lib/api";
import { toast } from "@/lib/toast";
import type { BootstrapData, SpendLimitSettings } from "@/types/flow";
import { SettingsCrumb, SettingsPageTitle } from "./settings-primitives";
import { LimitDialog } from "./spend-limit-dialog";
import { formatLimit, formatUsd, resetSchedule, resetsPhrase, timeZoneAbbreviation } from "./spend-limits-model";

type Entry = { id: string; name: string; email?: string; avatar?: { name: string; url?: string }; override: number };
type SortKey = "name" | "lastWeek" | "thisWeek" | "limit";

const RESET_LABEL = { daily: "Daily", weekly: "Weekly", monthly: "Monthly" } as const;

/** Per-user and per-loop spend overrides: who spends how much, and the limit each one runs under. */
export function UsageOverrides({ data, kind, onBack, onReload }: { data: BootstrapData; kind: "user" | "loop"; onBack: () => void; onReload: () => Promise<void> }) {
  const { t } = useI18n();
  const limits: SpendLimitSettings = data.workspaceSettings?.spendLimits ?? {};
  const zone = useMemo(() => timeZoneAbbreviation(), []);
  const schedule = resetSchedule(limits, data.workspace.createdAt);
  const reset = (kind === "user" ? limits.userReset : limits.loopReset) ?? "weekly";
  const inherited = (kind === "user" ? limits.userLimit : limits.loopLimit) ?? 0;
  const overrides = useMemo(() => (kind === "user" ? limits.userOverrides : limits.loopOverrides) ?? {}, [kind, limits.userOverrides, limits.loopOverrides]);
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<{ key: SortKey; descending: boolean }>({ key: "thisWeek", descending: true });
  const [overridesOnly, setOverridesOnly] = useState(false);
  const [editing, setEditing] = useState<Entry | undefined>();

  const entries: Entry[] = useMemo(() => {
    if (kind === "user") {
      return (data.members ?? [])
        .filter((member) => member.status === "active" && !member.user.app)
        .map((member) => ({
          id: member.user.id,
          name: member.user.displayName || member.user.name,
          email: member.user.email,
          avatar: { name: member.user.displayName || member.user.name, url: member.user.avatarUrl },
          override: overrides[member.user.id] ?? 0,
        }));
    }
    return (data.loops ?? []).filter((loop) => loop.status !== "draft" && loop.name).map((loop) => ({ id: loop.id, name: loop.name, override: overrides[loop.id] ?? 0 }));
  }, [data.members, data.loops, kind, overrides]);

  const visible = entries
    .filter((entry) => (!overridesOnly || entry.override > 0) && `${entry.name} ${entry.email ?? ""}`.toLowerCase().includes(query.trim().toLowerCase()))
    .sort((a, b) => {
      const key = sort.key === "limit" ? (a.override || inherited) - (b.override || inherited) : sort.key === "name" ? a.name.localeCompare(b.name) : 0;
      return sort.descending ? -key : key;
    });

  const save = async (id: string, amount: number) => {
    try {
      await updateWorkspacePreferences({ spendLimits: kind === "user" ? { userOverrides: { [id]: amount } } : { loopOverrides: { [id]: amount } } });
      await onReload();
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : t("Could not save spend limits"));
      throw reason;
    }
  };

  const header = (key: SortKey, label: string, className = "") => (
    <button
      type="button"
      className={`usage-th ${className}`}
      aria-label={`${t("Order by")} ${label}${sort.key === key ? `, ${sort.descending ? t("sorted descending") : t("sorted ascending")}` : ""}`}
      onClick={() => setSort((current) => ({ key, descending: current.key === key ? !current.descending : false }))}
    >
      {label}
      {sort.key === key && (sort.descending ? <ArrowDown size={12} aria-hidden="true" /> : <ArrowUp size={12} aria-hidden="true" />)}
    </button>
  );
  const subject = kind === "user" ? t("user") : t("loop");
  const phrase = resetsPhrase(reset, schedule, zone);
  return (
    <div className="usage-overrides">
      <SettingsCrumb onClick={onBack}>{t("Spend limits")}</SettingsCrumb>
      <SettingsPageTitle description={`${t(kind === "user" ? "View usage and set overrides for each user." : "View usage and set overrides for each loop.")} ${t("Limits reset")} ${phrase}.`}>
        {t("Usage")}
      </SettingsPageTitle>
      <div className="usage-overrides-toolbar">
        <label className="usage-search">
          <Search size={14} aria-hidden="true" />
          <input aria-label={t(kind === "user" ? "Search by name…" : "Search…")} placeholder={t(kind === "user" ? "Search by name…" : "Search…")} value={query} onChange={(event) => setQuery(event.target.value)} />
        </label>
        <button
          type="button"
          className={`usage-icon-button${overridesOnly ? " is-active" : ""}`}
          aria-label={t("Add filter")}
          aria-pressed={overridesOnly}
          title={t("Only show overrides")}
          onClick={() => setOverridesOnly(!overridesOnly)}
        >
          <ListFilter size={14} aria-hidden="true" />
        </button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button type="button" className="usage-icon-button usage-display-button" aria-label={t("Display options")}>
              <SlidersHorizontal size={14} aria-hidden="true" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {(["name", "lastWeek", "thisWeek", "limit"] as const).map((key) => (
              <DropdownMenuItem key={key} onSelect={() => setSort({ key, descending: key === "thisWeek" || key === "lastWeek" })}>
                {`${t("Order by")} ${t(key === "name" ? "Name" : key === "lastWeek" ? "Last week" : key === "thisWeek" ? "This week" : "Limit")}`}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      {entries.length === 0 ? (
        <p className="usage-empty">{kind === "user" ? t("No users yet") : t("No loops yet")}</p>
      ) : (
        <div className="usage-table" role="table" aria-label={t("Usage")}>
          <div className="usage-tr usage-thead" role="row">
            {header("name", t("Name"), "usage-col-name")}
            {header("lastWeek", t("Last week"))}
            {header("thisWeek", t("This week"))}
            {header("limit", `${t("Limit")} (${t(RESET_LABEL[reset])})`)}
            <span className="usage-th usage-th-static">{t("Limit used")}</span>
          </div>
          {visible.map((entry) => (
            <div className="usage-tr" role="row" key={entry.id}>
              <span className="usage-col-name" role="cell">
                {entry.avatar && <UserAvatar className="usage-avatar" name={entry.avatar.name} avatarUrl={entry.avatar.url} />}
                <span>
                  <strong data-i18n-ignore>{entry.name}</strong>
                  {entry.email && <small data-i18n-ignore>{entry.email}</small>}
                </span>
              </span>
              <span role="cell">{formatUsd(0)}</span>
              <span role="cell">{formatUsd(0)}</span>
              <span role="cell">
                <button type="button" className="usage-limit-cell" aria-label={`${t("Set limit")}: ${entry.name}`} onClick={() => setEditing(entry)}>
                  {entry.override ? formatLimit(entry.override) : inherited ? formatLimit(inherited) : t("No limit")}
                </button>
              </span>
              <span role="cell" className="usage-muted">{entry.override || inherited ? "0%" : "–"}</span>
            </div>
          ))}
          {!visible.length && <p className="usage-empty">{t("No results")}</p>}
        </div>
      )}
      {editing && (
        <LimitDialog
          title={`${t("Limit for")} ${editing.name}`}
          initial={editing.override || undefined}
          inheritedLabel={inherited ? `${t("Overrides the default")} ${subject} ${t("limit of")} ${formatLimit(inherited)}` : undefined}
          onClose={() => setEditing(undefined)}
          onSave={(amount) => save(editing.id, amount)}
        />
      )}
    </div>
  );
}
