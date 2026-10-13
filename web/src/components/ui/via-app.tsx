import { useOptionalTranslate } from "@/i18n/i18n";
import { viaAppName } from "@/lib/actor-attribution";
import type { ActorBot } from "@/types/flow";

import "./via-app.css";

/** Muted " via <App>" suffix shown next to the name of a record created through an application. */
export function ViaApp({ user, botActor }: { user?: { app?: boolean } | null; botActor?: ActorBot | null }) {
  const t = useOptionalTranslate();
  const app = viaAppName(user, botActor);
  if (!app) return null;
  return (
    <span className="via-app">
      {" "}
      {t("via")} <span data-i18n-ignore>{app}</span>
    </span>
  );
}
