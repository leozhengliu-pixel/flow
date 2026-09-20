import { useCallback, useEffect, useState, type ReactNode } from "react";
import { useLocation } from "react-router-dom";
import { GitBranch, Mail, MessageSquare, UserRound } from "lucide-react";
import type { AccountBootstrap, User, Workspace } from "@/types/flow";
import {
  authorizeIntegration,
  inviteMembers,
  updateAccountProfile,
} from "@/lib/api";
import {
  OnboardingNavigationProvider,
  OnboardingStepLayout,
} from "./onboarding-step-layout";
import "./welcome-onboarding.css";

export const WELCOME_STEPS = [
  "profile",
  "invite",
  "github",
  "slack",
] as const;

export type WelcomeStepId = (typeof WELCOME_STEPS)[number];

const STEP_LABELS = ["Profile", "Invite", "GitHub", "Slack"];

type Props = {
  account: AccountBootstrap;
  workspace: Workspace;
  viewer: User;
  onComplete: () => void;
  onNavigateState: (step: WelcomeStepId) => void;
};

function readStep(state: unknown): WelcomeStepId {
  const value =
    state && typeof state === "object" && "onboardingStep" in state
      ? String((state as { onboardingStep?: string }).onboardingStep)
      : "";
  return (WELCOME_STEPS as readonly string[]).includes(value)
    ? (value as WelcomeStepId)
    : "profile";
}

/** LS-0437 — post-create `/welcome` multi-step (profile → invite → GitHub → Slack). */
export function WelcomeOnboarding({
  account,
  workspace,
  viewer,
  onComplete,
  onNavigateState,
}: Props) {
  const location = useLocation();
  const [stepId, setStepId] = useState<WelcomeStepId>(() =>
    readStep(location.state),
  );
  const [displayName, setDisplayName] = useState(
    viewer.displayName || viewer.name || "",
  );
  const [jobTitle, setJobTitle] = useState(viewer.jobTitle || "");
  const [inviteEmails, setInviteEmails] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    setStepId(readStep(location.state));
  }, [location.state]);

  const step = Math.max(0, WELCOME_STEPS.indexOf(stepId));
  const goTo = useCallback(
    (next: WelcomeStepId) => {
      setStepId(next);
      setError("");
      onNavigateState(next);
    },
    [onNavigateState],
  );

  const finish = useCallback(() => {
    onComplete();
  }, [onComplete]);

  const nextStep = useCallback(() => {
    const next = WELCOME_STEPS[step + 1];
    if (next) goTo(next);
    else finish();
  }, [finish, goTo, step]);

  const skipStep = useCallback(() => {
    nextStep();
  }, [nextStep]);

  const goToStep = useCallback(
    (index: number) => {
      const target = WELCOME_STEPS[index];
      if (target) goTo(target);
    },
    [goTo],
  );

  const saveProfile = async () => {
    if (!displayName.trim()) {
      setError("Enter your name to continue");
      return;
    }
    setSaving(true);
    setError("");
    try {
      await updateAccountProfile({
        displayName: displayName.trim(),
        username:
          viewer.name ||
          displayName.trim().toLowerCase().replace(/\s+/g, "-"),
        jobTitle: jobTitle.trim() || undefined,
        avatarUrl: viewer.avatarUrl,
      });
      nextStep();
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : "Could not save profile",
      );
    } finally {
      setSaving(false);
    }
  };

  const sendInvites = async () => {
    const emails = inviteEmails
      .split(/[\s,;]+/)
      .map((value) => value.trim())
      .filter(Boolean);
    if (!emails.length) {
      nextStep();
      return;
    }
    setSaving(true);
    setError("");
    try {
      await inviteMembers(workspace.urlKey, {
        emails,
        role: "member",
        teamIds: [],
      });
      nextStep();
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : "Could not send invites",
      );
    } finally {
      setSaving(false);
    }
  };

  const connectProvider = async (provider: "github" | "slack") => {
    setSaving(true);
    setError("");
    try {
      await authorizeIntegration(provider, { name: provider });
    } catch (cause) {
      setError(
        cause instanceof Error
          ? cause.message
          : `Could not connect ${provider}`,
      );
      setSaving(false);
    }
  };

  let stepBody: ReactNode = null;
  if (stepId === "profile") {
    stepBody = (
      <OnboardingStepLayout
        icon={<UserRound size={18} />}
        title="Set up your profile"
        description="Tell your team who you are. You can change this later in settings."
        primaryLabel={saving ? "Saving…" : "Continue"}
        primaryDisabled={saving || !displayName.trim()}
        onPrimary={saveProfile}
      >
        <label>
          <span>Full name</span>
          <input
            autoFocus
            aria-label="Full name"
            value={displayName}
            onChange={(event) => setDisplayName(event.target.value)}
          />
        </label>
        <label>
          <span>Title</span>
          <input
            aria-label="Title"
            placeholder="Product designer"
            value={jobTitle}
            onChange={(event) => setJobTitle(event.target.value)}
          />
        </label>
        {error ? <div className="onboarding-step-error">{error}</div> : null}
      </OnboardingStepLayout>
    );
  } else if (stepId === "invite") {
    stepBody = (
      <OnboardingStepLayout
        icon={<Mail size={18} />}
        title="Invite your team"
        description="Add coworkers by email. You can skip and invite later from Members."
        primaryLabel={saving ? "Sending…" : "Continue"}
        primaryDisabled={saving}
        onPrimary={sendInvites}
      >
        <label>
          <span>Email addresses</span>
          <textarea
            autoFocus
            aria-label="Email addresses"
            placeholder="alex@acme.com, jordan@acme.com"
            value={inviteEmails}
            onChange={(event) => setInviteEmails(event.target.value)}
          />
        </label>
        {error ? <div className="onboarding-step-error">{error}</div> : null}
      </OnboardingStepLayout>
    );
  } else if (stepId === "github") {
    stepBody = (
      <OnboardingStepLayout
        icon={<GitBranch size={18} />}
        title="Connect GitHub"
        description="Link pull requests and reviews. Reuses workspace integration OAuth."
        primaryLabel={saving ? "Connecting…" : "Connect GitHub"}
        primaryDisabled={saving}
        onPrimary={() => connectProvider("github")}
      >
        <p className="welcome-integration-copy">
          Logged in as <strong>{account.viewer.email}</strong>. Connecting opens
          GitHub OAuth for this workspace.
        </p>
        {error ? <div className="onboarding-step-error">{error}</div> : null}
      </OnboardingStepLayout>
    );
  } else {
    stepBody = (
      <OnboardingStepLayout
        icon={<MessageSquare size={18} />}
        title="Connect Slack"
        description="Get issue notifications in Slack. Reuses workspace integration OAuth."
        primaryLabel={saving ? "Connecting…" : "Connect Slack"}
        primaryDisabled={saving}
        onPrimary={() => connectProvider("slack")}
        skipLabel="Finish"
      >
        <p className="welcome-integration-copy">
          You can finish setup without Slack and connect it later from Settings
          → Integrations.
        </p>
        {error ? <div className="onboarding-step-error">{error}</div> : null}
      </OnboardingStepLayout>
    );
  }

  return (
    <main className="welcome-onboarding" aria-label="Welcome">
      <div className="welcome-onboarding-brand">
        <strong>{workspace.name}</strong>
        <span>Welcome to Flow</span>
      </div>
      <OnboardingNavigationProvider
        step={step}
        count={WELCOME_STEPS.length}
        stepLabels={STEP_LABELS}
        goToStep={goToStep}
        nextStep={nextStep}
        skipStep={skipStep}
      >
        {stepBody}
      </OnboardingNavigationProvider>
    </main>
  );
}

export default WelcomeOnboarding;
