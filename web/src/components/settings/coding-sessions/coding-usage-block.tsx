import { useEffect, useState } from "react";
import { useI18n } from "@/i18n/i18n";
import { fetchCodingUsage } from "@/lib/api";
import type { CodingUsageSummary, User } from "@/types/flow";
import { formatMicros } from "./coding-sessions-model";
import "./coding-sessions.css";

/** Coding spend on Usage & limits (§20): totals, then the top spenders by user and by loop. */
export function CodingUsageBlock({ users, loopNames = {} }: { users: User[]; loopNames?: Record<string, string> }) {
  const { t } = useI18n();
  const [usage, setUsage] = useState<CodingUsageSummary>();
  useEffect(() => {
    let active = true;
    fetchCodingUsage()
      .then((next) => active && setUsage(next))
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, []);
  if (!usage) return null;
  const byUser = Object.entries(usage.byUser ?? {}).sort((a, b) => b[1] - a[1]).slice(0, 5);
  const byLoop = Object.entries(usage.byLoop ?? {}).sort((a, b) => b[1] - a[1]).slice(0, 5);
  const name = (id: string) => users.find((user) => user.id === id)?.name ?? id;
  const since = new Date(usage.since);
  return (
    <section className="usage-section cs-usage" data-testid="coding-usage">
      <header className="usage-section-header">
        <div>
          <h3>{t("Coding sessions spend")}</h3>
          <p>{t("Since {date}").replace("{date}", Number.isNaN(since.getTime()) ? usage.since : since.toLocaleDateString())}</p>
        </div>
      </header>
      <div className="usage-card usage-row usage-row-short">
        <strong>{t("Total")}</strong>
        <strong>{formatMicros(usage.totalCostMicros)}</strong>
      </div>
      <div className="usage-card usage-row usage-row-short">
        <span>{t("Model usage")} · {t("Sandbox runtime")} · {t("{count} sessions").replace("{count}", String(usage.sessions))}</span>
        <span data-i18n-ignore>{formatMicros(usage.llmCostMicros)} · {formatMicros(usage.runtimeCostMicros)}</span>
      </div>
      {byUser.length > 0 && (
        <div className="usage-card cs-usage-list">
          <strong>{t("By user")}</strong>
          {byUser.map(([id, micros]) => (
            <div key={id} className="cs-usage-item"><span data-i18n-ignore>{name(id)}</span><span>{formatMicros(micros)}</span></div>
          ))}
        </div>
      )}
      {byLoop.length > 0 && (
        <div className="usage-card cs-usage-list">
          <strong>{t("By loop")}</strong>
          {byLoop.map(([id, micros]) => (
            <div key={id} className="cs-usage-item"><span data-i18n-ignore>{loopNames[id] ?? id}</span><span>{formatMicros(micros)}</span></div>
          ))}
        </div>
      )}
    </section>
  );
}
