import { useCallback, useEffect, useState } from "react";
import { LoaderCircle } from "lucide-react";
import { useNavigate, useParams } from "react-router-dom";

import {
  fetchInviteLinkPreview,
  joinOrganization,
  logoutAccount,
} from "@/lib/api";
import type { AuthSession, InviteLinkPreview } from "@/types/flow";
import { LanguageSelect } from "@/i18n/i18n";

import "./auth-page.css";
import "./invite-link-accept.css";

type Props = {
  session: AuthSession | null;
  onJoined: (workspaceKey: string) => Promise<void>;
};

function initials(name: string) {
  return (
    name
      .split(/\s+/)
      .map((part) => part[0])
      .join("")
      .slice(0, 2)
      .toUpperCase() || "F"
  );
}

export function InviteLinkAccept({ session, onJoined }: Props) {
  const { token = "" } = useParams();
  const navigate = useNavigate();
  const [preview, setPreview] = useState<InviteLinkPreview | null>(null);
  const [pending, setPending] = useState(true);
  const [error, setError] = useState("");
  const [joining, setJoining] = useState(false);

  const handleJoined = useCallback(
    async (workspaceKey: string) => {
      await onJoined(workspaceKey);
    },
    [onJoined],
  );

  useEffect(() => {
    if (!token) {
      setError("This invite link is invalid or has expired");
      setPending(false);
      return;
    }
    let cancelled = false;
    setPending(true);
    fetchInviteLinkPreview(token)
      .then(async (details) => {
        if (cancelled) return;
        setPreview(details);
        if (details.alreadyMember) {
          await handleJoined(details.workspace.urlKey);
        }
      })
      .catch((reason) => {
        if (cancelled) return;
        setError(
          reason instanceof Error
            ? reason.message
            : "This invite link is invalid or has expired",
        );
      })
      .finally(() => {
        if (!cancelled) setPending(false);
      });
    return () => {
      cancelled = true;
    };
  }, [token, handleJoined]);

  const join = async () => {
    if (!preview) return;
    setJoining(true);
    setError("");
    try {
      await joinOrganization(preview.token);
      await handleJoined(preview.workspace.urlKey);
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Could not join workspace",
      );
    } finally {
      setJoining(false);
    }
  };

  return (
    <main className="auth-page invite-link-accept">
      <LanguageSelect className="auth-language" />
      <div className="auth-brand">
        <span className="auth-brand-mark" />
        Flow
      </div>
      <section className="auth-panel">
        <div className="auth-invite-mark" aria-hidden>
          {preview?.workspace.logoUrl ? (
            <img src={preview.workspace.logoUrl} alt="" />
          ) : (
            initials(preview?.workspace.name ?? "")
          )}
        </div>
        <h1>
          {preview
            ? `You’ve been invited to join the ${preview.workspace.name} workspace`
            : "Workspace invite"}
        </h1>
        {pending && !preview ? (
          <LoaderCircle className="auth-spinner" aria-label="Loading" />
        ) : null}
        {preview && !preview.alreadyMember ? (
          <>
            <p className="invite-link-accept-copy">
              Join to collaborate with your team in Flow.
            </p>
            {session ? (
              <button
                className="auth-primary"
                disabled={joining}
                onClick={() => void join()}
              >
                {joining ? (
                  <LoaderCircle className="auth-spinner" />
                ) : (
                  "Join workspace"
                )}
              </button>
            ) : (
              <div className="auth-invite-actions">
                <button
                  className="auth-primary"
                  onClick={() =>
                    navigate(
                      `/signup?returnTo=${encodeURIComponent(`/join/${token}`)}`,
                    )
                  }
                >
                  Create account
                </button>
                <button
                  className="auth-secondary"
                  onClick={() =>
                    navigate(
                      `/login?inviteLink=${encodeURIComponent(token)}&returnTo=${encodeURIComponent(`/join/${token}`)}`,
                    )
                  }
                >
                  Log in
                </button>
              </div>
            )}
            {session ? (
              <button
                className="auth-text-button"
                onClick={() =>
                  void logoutAccount().then(() =>
                    navigate(`/join/${token}`, { replace: true }),
                  )
                }
              >
                Sign out
              </button>
            ) : null}
          </>
        ) : null}
        {error ? (
          <div className="auth-error" role="alert">
            {error}
          </div>
        ) : null}
      </section>
    </main>
  );
}
