import type { ActorBot } from "@/types/flow";

type ActorUser = { displayName?: string; name?: string; avatarUrl?: string; app?: boolean } | null | undefined;

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
  const app = botActor?.name?.trim();
  if (!app) return undefined;
  // The application's own bot user never needs a "via" suffix on its own activity.
  if (user?.app && !botActor?.userDisplayName?.trim()) return undefined;
  return app;
}

export function attributedActorAvatar(user: ActorUser, botActor?: ActorBot | null): string | undefined {
  if (botActor?.userDisplayName?.trim()) return botActor.userAvatarUrl || botActor.avatarUrl || user?.avatarUrl;
  return user?.avatarUrl;
}

/** Plain-text form, "Mark via Zendesk". `via` is the (translated) connecting word. */
export function attributedActorLabel(user: ActorUser, botActor?: ActorBot | null, via = "via"): string {
  const name = attributedActorName(user, botActor);
  const app = viaAppName(user, botActor);
  return app ? `${name} ${via} ${app}` : name;
}
