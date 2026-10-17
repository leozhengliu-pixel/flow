import { ChevronLeft, ChevronRight, ListFilter } from "lucide-react";
import { useState } from "react";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData } from "@/types/flow";
import { SettingsCrumb, SettingsSelect } from "./settings-primitives";
import { formatUsd } from "./spend-limits-model";

type Row = { name: string; sessions: number; spend: number };

/** Settings › Usage & limits › Usage explorer: spend and sessions by team, feature or user over a period. */
export function UsageExplorer({ onBack }: { data: BootstrapData; onBack: () => void }) {
  const { t } = useI18n();
  const [period, setPeriod] = useState("30");
  const [measure, setMeasure] = useState("spend");
  const [groupBy, setGroupBy] = useState("team");
  // Flow does not meter AI spend yet, so there are no sessions to list.
  const rows: Row[] = [];
  const sessions = rows.reduce((sum, row) => sum + row.sessions, 0);
  const spend = rows.reduce((sum, row) => sum + row.spend, 0);
  return (
    <div className="usage-explorer">
      <SettingsCrumb onClick={onBack}>{t("Usage & limits")}</SettingsCrumb>
      <div className="usage-explorer-main">
        <header className="usage-explorer-stats">
          <span><b>{sessions}</b> {sessions === 1 ? t("session") : t("sessions")}</span>
          <span><b>{formatUsd(sessions ? spend / sessions : 0)}</b> {t("average")}</span>
          <span><b>{formatUsd(spend)}</b> {t("total spend")}</span>
          <div className="usage-explorer-filters">
            <SettingsSelect
              className="settings-select usage-period-select"
              label={t("Period")}
              value={period}
              options={[
                { value: "7", label: t("Last 7 days") },
                { value: "30", label: t("Last 30 days") },
                { value: "90", label: t("Last 90 days") },
              ]}
              onChange={setPeriod}
            />
            <button type="button" className="usage-icon-button" aria-label={t("Add filter")} disabled title={t("There is no usage to filter yet")}>
              <ListFilter size={14} aria-hidden="true" />
            </button>
          </div>
        </header>
        <div className="usage-explorer-body" />
        <footer className="usage-explorer-footer">
          <button type="button" className="usage-page-button" aria-label={t("Previous page")} disabled>
            <ChevronLeft size={14} aria-hidden="true" />
          </button>
          <button type="button" className="usage-page-button" aria-label={t("Next page")} disabled>
            <ChevronRight size={14} aria-hidden="true" />
          </button>
          <span>{rows.length === 1 ? t("1 result") : `${rows.length} ${t("results")}`}</span>
          <button type="button" className="usage-export" disabled={!rows.length}>
            {t("Export CSV")}
          </button>
        </footer>
      </div>
      <aside className="usage-explorer-side">
        <label>
          <span>{t("Measure")}</span>
          <SettingsSelect
            className="settings-select usage-side-select"
            label={t("Measure")}
            value={measure}
            options={[
              { value: "spend", label: t("Spend") },
              { value: "sessions", label: t("Sessions") },
            ]}
            onChange={setMeasure}
          />
        </label>
        <label>
          <span>{t("Group by")}</span>
          <SettingsSelect
            className="settings-select usage-side-select"
            label={t("Group by")}
            value={groupBy}
            options={[
              { value: "team", label: t("Team") },
              { value: "feature", label: t("Feature") },
              { value: "user", label: t("User") },
            ]}
            onChange={setGroupBy}
          />
        </label>
      </aside>
    </div>
  );
}
