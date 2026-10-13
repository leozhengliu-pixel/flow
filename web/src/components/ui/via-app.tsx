import { Fragment, type ReactNode } from "react";

import { useOptionalTranslate } from "@/i18n/i18n";
import { viaAppName, type ActivityActor } from "@/lib/actor-attribution";
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

/** The phrase a history row uses for Flow's agent: "{actor} {action} on behalf of {user}" (zh-CN puts the person first). */
export const ON_BEHALF_OF_TEMPLATE = "{actor} {action} on behalf of {user}";

/**
 * A history sentence with its actor in bold: "Dev User via Zendesk set priority to Urgent", or for Flow's agent
 * "Flow set priority to Urgent on behalf of Dev User" (zh-CN "Flow 代表 Dev User 将优先级设为 紧急").
 */
export function AttributedAction({ actor, children }: { actor: ActivityActor; children: ReactNode }) {
  const t = useOptionalTranslate();
  const name = <strong data-i18n-ignore>{actor.name}</strong>;
  if (!actor.onBehalfOf) {
    return (
      <>
        {name}
        {actor.via && (
          <span className="via-app">
            {" "}
            {t("via")} <span data-i18n-ignore>{actor.via}</span>
          </span>
        )}{" "}
        {children}
      </>
    );
  }
  const values: Record<string, ReactNode> = {
    actor: name,
    action: children,
    user: (
      <span className="attributed-action__behalf" data-i18n-ignore>
        {actor.onBehalfOf}
      </span>
    ),
  };
  const template = t(ON_BEHALF_OF_TEMPLATE);
  const parts: ReactNode[] = [];
  let last = 0;
  for (const match of template.matchAll(/\{(actor|action|user)\}/g)) {
    if (match.index > last) parts.push(template.slice(last, match.index));
    parts.push(values[match[1]]);
    last = match.index + match[0].length;
  }
  if (last < template.length) parts.push(template.slice(last));
  return (
    <span className="attributed-action" data-agent-attribution="">
      {parts.map((part, index) => (
        <Fragment key={index}>{part}</Fragment>
      ))}
    </span>
  );
}
