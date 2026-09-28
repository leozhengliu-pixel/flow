import { useEffect, useState } from "react";
import { LogOut, ShieldAlert } from "lucide-react";
import { useI18n } from "@/i18n/i18n";
import {
  fetchWorkspaceAccessStatus,
  logoutAccount,
  type WorkspaceAccessStatus,
} from "@/lib/api";
import "./organization-not-found.css";

type Props = {
  orgKey: string;
  onLogout?: () => Promise<void> | void;
};

/**
 * LS-0442 OrganizationNotFound — branched workspace gate for missing
 * membership / auth restriction / non-existent url keys.
 */
export function OrganizationNotFound({ orgKey, onLogout }: Props) {
  const { t } = useI18n();
  const [status, setStatus] = useState<WorkspaceAccessStatus | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void fetchWorkspaceAccessStatus(orgKey)
      .then((next) => {
        if (!cancelled) setStatus(next);
      })
      .catch(() => {
        if (!cancelled)
          setStatus({
            exists: false,
            hasMembership: false,
            reason: "not_found",
            allowedAuthServices: [],
            allowedAuthLabels: [],
          });
      });
    return () => {
      cancelled = true;
    };
  }, [orgKey]);

  const logout = async () => {
    setBusy(true);
    try {
      if (onLogout) await onLogout();
      else {
        await logoutAccount();
        location.assign("/login");
      }
    } finally {
      setBusy(false);
    }
  };

  if (!status) {
    return (
      <main className="organization-not-found" aria-busy="true">
        <div className="organization-not-found__card" />
      </main>
    );
  }

  const reason = status.reason === "ok" && !status.hasMembership ? "no_access" : status.reason;
  const labels = status.allowedAuthLabels?.length
    ? status.allowedAuthLabels
    : status.allowedAuthServices;

  let title = t("This workspace does not exist.");
  let body: string | null = null;
  let showLogout = false;

  if (reason === "auth_restricted") {
    title = t("Login method not allowed");
    const methods =
      labels.length > 0 ? labels.join(` ${t("or")} `) : t("an allowed authentication method");
    body = t(
      "You can only switch to this workspace when logged in with {methods}. Please logout and login with the correct authentication method.",
    ).replace("{methods}", methods);
    showLogout = true;
  } else if (reason === "no_access") {
    title = t("You don’t have access to this workspace.");
    body = t("Please contact a workspace admin for access.");
    showLogout = true;
  }

  return (
    <main className="organization-not-found">
      <div className="organization-not-found__card">
        <ShieldAlert size={28} aria-hidden />
        <h1>{title}</h1>
        {body ? <p>{body}</p> : null}
        {reason === "auth_restricted" && labels.length > 0 ? (
          <ul className="organization-not-found__methods">
            {labels.map((label) => (
              <li key={label}>{label}</li>
            ))}
          </ul>
        ) : null}
        {showLogout ? (
          <button type="button" className="organization-not-found__logout" disabled={busy} onClick={() => void logout()}>
            <LogOut size={14} />
            {t("Log out")}
          </button>
        ) : null}
      </div>
    </main>
  );
}
