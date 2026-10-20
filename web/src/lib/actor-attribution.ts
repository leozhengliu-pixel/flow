import type { ActorBot } from "@/types/flow";

type ActorUser = { displayName?: string; name?: string; avatarUrl?: string; app?: boolean } | null | undefined;

type ApplicationUser = { app?: boolean; oauthClientId?: string; displayName?: string; name?: string; avatarUrl?: string };

// The current name and logo of each installed OAuth application, by client
// ID, from the workspace's app users. Records keep the application's name and
// logo as they were when written; these show the application as it is now.
let liveApplications = new Map<string, { name: string; avatarUrl?: string }>();

export function setLiveApplicationIdentities(users: readonly ApplicationUser[] | null | undefined): void {
  const next = new Map<string, { name: string; avatarUrl?: string }>();
  for (const user of users ?? []) {
    const name = user.displayName?.trim() || user.name?.trim();
    if (user.app && user.oauthClientId && name) next.set(user.oauthClientId, { name, avatarUrl: user.avatarUrl || undefined });
  }
  liveApplications = next;
}

/** The record's application attribution with the application's current name and logo. */
export function liveBotActor<T extends ActorBot | null | undefined>(botActor: T): T {
  if (!botActor || botActor.type !== "oauthApplication" || !botActor.id) return botActor;
  const current = liveApplications.get(botActor.id);
  if (!current || (current.name === botActor.name && current.avatarUrl === botActor.avatarUrl)) return botActor;
  return { ...botActor, name: current.name, avatarUrl: current.avatarUrl };
}

/**
 * "<User> via <App>" attribution (like the reference app's "Mark via Zendesk"):
 * - An application acting on behalf of an external person (`userDisplayName`) names that person.
 * - Otherwise the record's own user is named and the application shown after "via".
 */
export function attributedActorName(user: ActorUser, botActor?: ActorBot | null): string {
  return botActor?.userDisplayName?.trim() || user?.displayName || user?.name || "";
}

/** The application name to show after "via", if any. */
export function viaAppName(user: ActorUser, botActor?: ActorBot | null): string | undefined {
  const app = liveBotActor(botActor)?.name?.trim();
  if (!app) return undefined;
  // The application's own bot user never needs a "via" suffix on its own activity.
  if (user?.app && !botActor?.userDisplayName?.trim()) return undefined;
  return app;
}

export function attributedActorAvatar(user: ActorUser, botActor?: ActorBot | null): string | undefined {
  if (botActor?.userDisplayName?.trim()) return botActor.userAvatarUrl || liveBotActor(botActor).avatarUrl || user?.avatarUrl;
  return user?.avatarUrl;
}

/** Plain-text form, "Mark via Zendesk". `via` is the (translated) connecting word. */
export function attributedActorLabel(user: ActorUser, botActor?: ActorBot | null, via = "via"): string {
  const name = attributedActorName(user, botActor);
  const app = viaAppName(user, botActor);
  return app ? `${name} ${via} ${app}` : name;
}

/** Flow's agent wrote the record for a person ("Flow set priority to Urgent on behalf of Dev User"). */
export function isAgentBot(botActor?: ActorBot | null): botActor is ActorBot {
  return botActor?.type === "agent";
}

type DirectoryUser = { id: string; displayName?: string; name?: string };

/**
 * Who a history row names. Flow's agent rows name the agent (with its avatar) and the person it acted for;
 * every other row names the person, with the application after "via" when there is one.
 */
export interface ActivityActor {
  name: string;
  avatarUrl?: string;
  /** Set on Flow's agent rows: the person the agent acted for. */
  onBehalfOf?: string;
  /** The application after "via" on rows that are not the agent's. */
  via?: string;
}

export function activityActor(user: ActorUser, botActor?: ActorBot | null, users?: DirectoryUser[]): ActivityActor {
  if (isAgentBot(botActor)) {
    const person = botActor.onBehalfOfId ? users?.find((item) => item.id === botActor.onBehalfOfId) : undefined;
    const fallback = user?.app ? "" : user?.displayName || user?.name || "";
    const onBehalfOf = person?.displayName || person?.name || botActor.onBehalfOfName?.trim() || fallback;
    return { name: botActor.name?.trim() || "Flow", avatarUrl: botActor.avatarUrl, onBehalfOf: onBehalfOf || undefined };
  }
  return { name: attributedActorName(user, botActor), avatarUrl: attributedActorAvatar(user, botActor), via: viaAppName(user, botActor) };
}

/** Same actor and attribution: consecutive history rows by it may be shown as one. */
export function sameActivityActor(
  left: { actor?: { id?: string } | null; botActor?: ActorBot | null },
  right: { actor?: { id?: string } | null; botActor?: ActorBot | null },
): boolean {
  const key = (item: typeof left) => [item.actor?.id ?? "", item.botActor?.type ?? "", item.botActor?.id ?? "", item.botActor?.onBehalfOfId ?? "", item.botActor?.userDisplayName ?? ""].join("\u0000");
  return key(left) === key(right);
}

/**
 * A row's text after its bold actor name, for lists that render the actor separately: "set priority to High", or for
 * Flow's agent "set priority to High on behalf of Dev User" (zh-CN "代表 Dev User 将优先级设为 高").
 */
export function attributedActionText(actor: ActivityActor, action: string, t: (text: string) => string = (text) => text): string {
  if (!actor.onBehalfOf) return action;
  const template = t("{actor} {action} on behalf of {user}").replace(/^\{actor\}\s*/, "");
  return template.replace("{action}", action).replace("{user}", actor.onBehalfOf);
}
