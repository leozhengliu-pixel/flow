import { AlertCircle, RefreshCw, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "@/lib/toast";

import { Toggle } from "@/components/ui/toggle";
import { useI18n } from "@/i18n/i18n";
import { fetchReviewImport, importReviews, updateReviewImport } from "@/lib/api";
import { isWorkspaceAdmin } from "@/lib/settings-access";
import type { BootstrapData, ReviewImportRepository, ReviewImportStatus } from "@/types/flow";

import "./review-import-settings.css";

/**
 * Importing open pull/merge requests without webhooks: off until a workspace
 * admin turns it on, and limited to the repositories they approve here. The
 * server may turn it off for everyone (FLOW_REVIEW_IMPORT).
 */
export function ReviewImportSettings({ provider, data, className }: { provider: "github" | "gitlab"; data: BootstrapData; className?: string }) {
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
  const backoffs = status.backoffs.filter((item) => item.target.startsWith(`${provider}:`));

  return (
    <section className={`review-import-settings ${className ?? ""}`} aria-labelledby={`review-import-${provider}`}>
      <header>
        <h2 id={`review-import-${provider}`}>{t("Import pull requests without webhooks")}</h2>
        <p>{t("Flow reads the open pull and merge requests of approved repositories only. Use it when webhooks cannot reach Flow.")}</p>
      </header>
      {!status.available && (
        <p className="review-import-notice" role="note">
          <AlertCircle aria-hidden="true" />
          {t("Turned off on this server. An administrator of the deployment can allow it with FLOW_REVIEW_IMPORT=on.")}
        </p>
      )}
      <div className="review-import-row">
        <div>
          <strong>{t("Import approved repositories")}</strong>
          <span>
            {t("Runs every {minutes} minutes and with Sync now; nothing outside the approved list is read.").replace("{minutes}", String(Math.round(status.interval / 60)))}
          </span>
        </div>
        <Toggle checked={status.enabled} disabled={!admin || busy} label={t("Import approved repositories")} onChange={(enabled) => void save({ enabled })} size="regular" />
      </div>
      <div className="review-import-repositories">
        <strong>{t("Approved repositories")}</strong>
        {approved.length ? (
          <ul>
            {approved.map((item) => (
              <li key={item.repository}>
                <span data-i18n-ignore>{item.repository}</span>
                {admin && (
                  <button type="button" aria-label={`${t("Remove")} ${item.repository}`} disabled={busy} onClick={() => remove(item.repository)}>
                    <X aria-hidden="true" />
                  </button>
                )}
              </li>
            ))}
          </ul>
        ) : (
          <span className="review-import-empty">{t("No repositories approved. Nothing is imported until you add one.")}</span>
        )}
        {admin && (
          <form
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
            <button type="submit" className="review-import-button" disabled={busy || !draft.trim()}>
              {t("Approve")}
            </button>
          </form>
        )}
      </div>
      <div className="review-import-status" aria-live="polite">
        <div className="review-import-status-head">
          <strong>{t("Last run")}</strong>
          {admin && status.active && (
            <button type="button" className="review-import-button" disabled={syncing || status.running} aria-busy={syncing} onClick={() => void syncNow()}>
              <RefreshCw aria-hidden="true" className={syncing ? "review-import-spinning" : undefined} />
              {t("Sync now")}
            </button>
          )}
        </div>
        {status.running && <span>{t("Running…")}</span>}
        {last ? (
          <>
            <span>
              {dateTime(last.startedAt)} – {dateTime(last.finishedAt)}
              {last.partial ? ` · ${t("Stopped at a limit; the next run continues")}` : ""}
            </span>
            <dl>
              <div><dt>{t("Scanned")}</dt><dd>{last.scanned ?? 0}</dd></div>
              <div><dt>{t("Imported")}</dt><dd>{last.imported}</dd></div>
              <div><dt>{t("Updated")}</dt><dd>{last.updated}</dd></div>
              <div><dt>{t("Unchanged")}</dt><dd>{last.skippedUnchanged ?? 0}</dd></div>
              <div><dt>{t("Failed")}</dt><dd>{last.failed ?? 0}</dd></div>
            </dl>
          </>
        ) : (
          <span>{t("Not run yet")}</span>
        )}
        {status.nextRunAt && <span>{t("Next run")}: {dateTime(status.nextRunAt)}</span>}
        {status.rateLimitedUntil && <span>{t("Rate limited until")}: {dateTime(status.rateLimitedUntil)}</span>}
        {backoffs.length > 0 && (
          <ul className="review-import-backoffs" aria-label={t("Retrying later")}>
            {backoffs.map((item) => (
              <li key={item.target}>
                <span data-i18n-ignore>{item.target.replace(/^(github|gitlab):/, "")}</span>
                <span>{item.message}</span>
                {item.backoffUntil && <span>{t("Retry after")}: {dateTime(item.backoffUntil)}</span>}
              </li>
            ))}
          </ul>
        )}
        <span className="review-import-limits">
          {t("Each run reads up to {repositories} repositories and {requests} requests and makes at most {writes} changes.")
            .replace("{repositories}", String(status.limits.repositoriesPerRun))
            .replace("{requests}", String(status.limits.requestsPerRun))
            .replace("{writes}", String(status.limits.writesPerRun))}
        </span>
      </div>
    </section>
  );
}
