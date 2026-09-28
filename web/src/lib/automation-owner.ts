/**
 * LS-0091 Automation owner helpers — effectiveOwner + change-owner action.
 */
import type { User } from "@/types/flow";

export type AutomationOwnerAccess = {
  configure: { kind: "allowed" | "denied"; reason?: string };
};

export type ChangeAutomationOwnerAction = {
  id: "change-automation-owner";
  label: string;
  allowed: boolean;
  reason?: string;
};

export function getChangeAutomationOwnerAction(
  access: AutomationOwnerAccess | undefined | null,
): ChangeAutomationOwnerAction {
  const allowed = access?.configure.kind === "allowed";
  return {
    id: "change-automation-owner",
    label: "Change owner",
    allowed,
    reason: allowed ? undefined : access?.configure.reason ?? "You cannot change the owner",
  };
}

/** Resolve effective owner: explicit ownerId → creator → fallback viewer. */
export function resolveEffectiveOwner(input: {
  ownerId?: string | null;
  creatorId?: string | null;
  creator?: User | null;
  users: User[];
  viewer?: User | null;
}): User {
  const byId = (id: string | undefined | null) =>
    id ? input.users.find((user) => user.id === id) : undefined;
  return (
    byId(input.ownerId) ||
    input.creator ||
    byId(input.creatorId) ||
    input.viewer ||
    input.users[0] || {
      id: "unknown",
      name: "Unknown",
      displayName: "Unknown",
      email: "",
      active: false,
      emailVerified: false,
    }
  );
}

/** Default configure access for workspace members managing automations. */
export function automationOwnerAccess(
  viewerRole: string | undefined | null,
): AutomationOwnerAccess {
  const role = String(viewerRole ?? "").toLowerCase();
  if (role === "guest" || role === "") {
    return {
      configure: {
        kind: "denied",
        reason: "Guests cannot change automation owners",
      },
    };
  }
  return { configure: { kind: "allowed" } };
}
