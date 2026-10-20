import { GitPullRequest, Plus, Unplug } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { useI18n } from "@/i18n/i18n";
import {
  fetchGitHubApp,
  removeGitHubAppInstallation,
  startGitHubAppInstall,
} from "@/lib/coding-sessions-api";
import { toast } from "@/lib/toast";
import type { GitHubAppStatus } from "@/types/coding-sessions";

/**
 * GitHub App card on the GitHub integration page (coding sessions track D):
 * install the App for an account and unlink installations from the workspace.
 * Hidden when the server has no App configured.
 */
export function GitHubAppCard({ isAdmin }: { isAdmin: boolean }) {
  const { t } = useI18n();
  const [status, setStatus] = useState<GitHubAppStatus | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      setStatus(await fetchGitHubApp());
    } catch {
      setStatus(null);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (!status?.configured) return null;

  const redirectTo = async (start: () => Promise<{ url: string }>) => {
    setBusy(true);
    try {
      const { url } = await start();
      window.location.assign(url);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not reach GitHub"));
      setBusy(false);
    }
  };

  const unlinkInstallation = async (id: string) => {
    setBusy(true);
    try {
      await removeGitHubAppInstallation(id);
      await load();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not remove the installation"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="code-integration-section" aria-labelledby="github-app-heading">
      <div className="code-section-title">
        <div>
          <h2 id="github-app-heading">{t("GitHub App")}</h2>
          <p>{t("Coding sessions use the GitHub App to push branches and open draft pull requests with short-lived tokens.")}</p>
        </div>
        {isAdmin && (
          <button className="code-primary" disabled={busy} onClick={() => void redirectTo(startGitHubAppInstall)}>
            <Plus />
            {t("Install GitHub App")}
          </button>
        )}
      </div>
      {status.installations.map((installation) => (
        <div className="code-connection-row" key={installation.id}>
          <div className="code-provider-avatar github">
            <GitPullRequest />
          </div>
          <div>
            <strong data-i18n-ignore>{installation.accountLogin}</strong>
            <span>
              {installation.suspendedAt
                ? t("Suspended")
                : installation.repositorySelection === "all"
                  ? t("All repositories")
                  : t("Selected repositories")}
            </span>
          </div>
          {isAdmin && (
            <button
              className="code-manage"
              disabled={busy}
              aria-label={t("Remove installation")}
              onClick={() => void unlinkInstallation(installation.id)}
            >
              <Unplug />
              {t("Remove")}
            </button>
          )}
        </div>
      ))}
    </section>
  );
}
