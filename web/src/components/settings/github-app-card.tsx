import { GitPullRequest, Link2, Plus, Unplug } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { useI18n } from "@/i18n/i18n";
import {
  fetchAvailableGitHubAppInstallations,
  fetchGitHubApp,
  linkGitHubAppInstallation,
  removeGitHubAppInstallation,
  startGitHubAppInstall,
} from "@/lib/coding-sessions-api";
import { toast } from "@/lib/toast";
import type { GitHubAppInstallationSummary, GitHubAppStatus } from "@/types/coding-sessions";

/**
 * GitHub App card on the GitHub integration page (coding sessions track D):
 * install the App for an account and unlink installations from the workspace.
 * Hidden when the server has no App configured.
 */
export function GitHubAppCard({ isAdmin }: { isAdmin: boolean }) {
  const { t } = useI18n();
  const [status, setStatus] = useState<GitHubAppStatus | null>(null);
  const [busy, setBusy] = useState(false);
  // Installations the admin's own GitHub account can access but the
  // workspace has not linked: GitHub sends no setup redirect when the App is
  // already installed and nothing changes, so these are linked from here.
  const [available, setAvailable] = useState<GitHubAppInstallationSummary[]>([]);

  const load = useCallback(async () => {
    let next: GitHubAppStatus | null = null;
    try {
      next = await fetchGitHubApp();
    } catch {
      next = null;
    }
    setStatus(next);
    if (!isAdmin || !next?.configured || !next.linkedAccount) {
      setAvailable([]);
      return;
    }
    try {
      setAvailable((await fetchAvailableGitHubAppInstallations()).installations);
    } catch {
      setAvailable([]);
    }
  }, [isAdmin]);

  useEffect(() => {
    // load() flips the loading state up front; start it from a microtask so the effect itself never sets state.
    queueMicrotask(() => { void load(); });
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

  const linkInstallation = async (installationId: string) => {
    setBusy(true);
    try {
      await linkGitHubAppInstallation(installationId);
      await load();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not link the installation"));
    } finally {
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
      {isAdmin &&
        available.map((installation) => (
          <div className="code-connection-row" key={`available-${installation.installationId}`}>
            <div className="code-provider-avatar github">
              <GitPullRequest />
            </div>
            <div>
              <strong data-i18n-ignore>{installation.accountLogin}</strong>
              <span>{t("Installed on GitHub, not linked to this workspace")}</span>
            </div>
            <button
              className="code-manage"
              disabled={busy}
              aria-label={t("Link installation")}
              onClick={() => void linkInstallation(installation.installationId)}
            >
              <Link2 />
              {t("Link")}
            </button>
          </div>
        ))}
    </section>
  );
}
