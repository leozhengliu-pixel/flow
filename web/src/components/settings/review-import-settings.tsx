import { AlertCircle, RefreshCw, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "@/lib/toast";

import { useI18n } from "@/i18n/i18n";
import { fetchReviewImport, importReviews, updateReviewImport } from "@/lib/api";
import { isWorkspaceAdmin } from "@/lib/settings-access";
import type { BootstrapData, ReviewImportRepository, ReviewImportStatus } from "@/types/flow";

import { FeatureButton, FeatureCard, FeatureRow, FeatureSection, Toggle } from "./feature-primitives";

import "./feature-settings.css";
import "./review-import-settings.css";

/**
 * Importing open pull/merge requests without webhooks: off until a workspace
 * admin turns it on, and limited to the repositories they approve here. The
 * server may turn it off for everyone (FLOW_REVIEW_IMPORT).
 */
export function ReviewImportSettings({ provider, data }: { provider: "github" | "gitlab"; data: BootstrapData }) {
  const { t, formatDate } = useI18n();
  const admin = isWorkspaceAdmin(data);
  const [status, setStatus] = useState<ReviewImportStatus>();
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [syncing, setSyncing] = useState(false);

  useEffect(() => {
    let active = true;
    fetchReviewImport()
      .then((next) => active && setStatus(next))
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, []);

  const approved = (status?.repositories ?? []).filter((item) => item.provider === provider);
  const suggestions = useMemo(() => {
    const names = new Set<string>();
    for (const review of data.reviews ?? []) {
      if (review.provider === provider && review.repositoryOwner && review.repositoryName) names.add(`${review.repositoryOwner}/${review.repositoryName}`);
    }
    for (const item of status?.repositories ?? []) names.delete(item.repository);
    return [...names].sort();
  }, [data.reviews, provider, status?.repositories]);

  const save = async (input: { enabled?: boolean; repositories?: ReviewImportRepository[] }) => {
    setBusy(true);
    try {
      setStatus(await updateReviewImport(input));
      return true;
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not update the pull request import"));
      return false;
    } finally {
      setBusy(false);
    }
  };
  const others = (status?.repositories ?? []).filter((item) => item.provider !== provider);
  const add = async () => {
    const name = draft.trim();
    if (!name) return;
    if (await save({ repositories: [...others, ...approved, { provider, repository: name }] })) setDraft("");
  };
  const remove = (name: string) => void save({ repositories: [...others, ...approved.filter((item) => item.repository !== name)] });
  const syncNow = async () => {
    setSyncing(true);
    try {
      const result = await importReviews();
      if (result.rateLimitedUntil) toast.error(t("The code host's rate limit is reached; the rest syncs later"));
      else if (result.errors.length) toast.error(`${t("Some repositories could not be synced")}: ${result.errors[0]}`);
      else toast.success(t("Synced: {count} new reviews").replace("{count}", String(result.imported)));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not sync with the code host"));
    } finally {
      setSyncing(false);
      fetchReviewImport().then(setStatus).catch(() => undefined);
    }
  };

  if (!status) return null;
  const last = status.last;
  const placeholder = provider === "github" ? "owner/repository" : "group/project";
  const dateTime = (value: string) => formatDate(value, { dateStyle: "medium", timeStyle: "short" });
  // A run that started and finished the same day names the date once.
  const runRange = (start: string, end: string) =>
    new Date(start).toDateString() === new Date(end).toDateString()
      ? `${dateTime(start)} – ${formatDate(end, { timeStyle: "short" })}`
      : `${dateTime(start)} – ${dateTime(end)}`;
  const backoffs = status.backoffs.filter((item) => item.target.startsWith(`${provider}:`));

  return (
    <div className="review-import-settings">
      <FeatureSection title="Import pull requests without webhooks" description="Flow reads the open pull and merge requests of approved repositories only. Use it when webhooks cannot reach Flow.">
        <FeatureCard>
          <FeatureRow
            title="Import approved repositories"
            description={t("Runs every {minutes} minutes and with Sync now; nothing outside the approved list is read.").replace("{minutes}", String(Math.round(status.interval / 60)))}
          >
            <Toggle checked={status.enabled} disabled={!admin || busy || !status.available} label="Import approved repositories" onChange={(enabled) => void save({ enabled })} />
          </FeatureRow>
          {!status.available && (
            <div className="feature-row" role="note">
              <span className="feature-row-icon"><AlertCircle size={16} aria-hidden="true" /></span>
              <div>
                <span>{t("Turned off on this server. An administrator of the deployment can allow it with FLOW_REVIEW_IMPORT=on.")}</span>
              </div>
            </div>
          )}
        </FeatureCard>
      </FeatureSection>
      <FeatureSection title="Approved repositories">
        <FeatureCard>
          {approved.map((item) => (
            <FeatureRow key={item.repository} title={item.repository} businessTitle>
              {admin && (
                <FeatureButton type="button" aria-label={`${t("Remove")} ${item.repository}`} disabled={busy} onClick={() => remove(item.repository)}>
                  <X size={14} aria-hidden="true" />
                </FeatureButton>
              )}
            </FeatureRow>
          ))}
          {!approved.length && (
            <div className="feature-row">
              <div><span>{t("No repositories approved. Nothing is imported until you add one.")}</span></div>
            </div>
          )}
          {admin && (
            <form
              className="feature-row review-import-add"
              onSubmit={(event) => {
                event.preventDefault();
                void add();
              }}
            >
              <input
                aria-label={t("Repository to approve")}
                placeholder={placeholder}
                value={draft}
                list={`review-import-suggestions-${provider}`}
                onChange={(event) => setDraft(event.target.value)}
              />
              <datalist id={`review-import-suggestions-${provider}`}>
                {suggestions.map((name) => (
                  <option key={name} value={name} />
                ))}
              </datalist>
              <FeatureButton type="submit" disabled={busy || !draft.trim()}>
                Approve
              </FeatureButton>
            </form>
          )}
        </FeatureCard>
      </FeatureSection>
      <FeatureSection title="Last run">
        <FeatureCard>
          <div aria-live="polite" className="review-import-status">
            <FeatureRow
              businessTitle
              title={
                status.running
                  ? t("Running…")
                  : last
                    ? runRange(last.startedAt, last.finishedAt)
                    : t("Not run yet")
              }
              description={last?.partial && !status.running ? t("Stopped at a limit; the next run continues") : undefined}
            >
              {admin && status.active && (
                <FeatureButton type="button" disabled={syncing || status.running} aria-busy={syncing} onClick={() => void syncNow()}>
                  <RefreshCw size={14} aria-hidden="true" className={syncing ? "review-import-spinning" : undefined} />
                  Sync now
                </FeatureButton>
              )}
            </FeatureRow>
            {last && (
              <div className="feature-row">
                <dl>
                  <div><dt>{t("Scanned")}</dt><dd>{last.scanned ?? 0}</dd></div>
                  <div><dt>{t("Imported")}</dt><dd>{last.imported}</dd></div>
                  <div><dt>{t("Updated")}</dt><dd>{last.updated}</dd></div>
                  <div><dt>{t("Unchanged")}</dt><dd>{last.skippedUnchanged ?? 0}</dd></div>
                  <div><dt>{t("Failed")}</dt><dd>{last.failed ?? 0}</dd></div>
                </dl>
              </div>
            )}
            {status.nextRunAt && <FeatureRow title="Next run" description={dateTime(status.nextRunAt)} />}
            {status.rateLimitedUntil && <FeatureRow title="Rate limited until" description={dateTime(status.rateLimitedUntil)} />}
            {backoffs.length > 0 && (
              <ul className="review-import-backoffs" aria-label={t("Retrying later")}>
                {backoffs.map((item) => (
                  <li key={item.target} className="feature-row">
                    <div>
                      <strong data-i18n-ignore>{item.target.replace(/^(github|gitlab):/, "")}</strong>
                      <span>{item.message}</span>
                      {item.backoffUntil && <span>{t("Retry after: {time}").replace("{time}", dateTime(item.backoffUntil))}</span>}
                    </div>
                  </li>
                ))}
              </ul>
            )}
            <div className="feature-row">
              <div>
                <span>
                  {t("Each run reads up to {repositories} repositories and {requests} requests and makes at most {writes} changes.")
                    .replace("{repositories}", String(status.limits.repositoriesPerRun))
                    .replace("{requests}", String(status.limits.requestsPerRun))
                    .replace("{writes}", String(status.limits.writesPerRun))}
                </span>
              </div>
            </div>
          </div>
        </FeatureCard>
      </FeatureSection>
    </div>
  );
}
