import { AppWindow } from "lucide-react";

import type { OAuthApplication } from "@/types/flow";

import "./application-header.css";

export function OAuthAppImage({
  app,
  size = 40,
}: {
  app: Pick<OAuthApplication, "name"> & { logoUrl?: string };
  size?: number;
}) {
  const initial = (app.name.trim()[0] ?? "A").toUpperCase();
  return (
    <span
      className="oauth-app-image"
      style={{ width: size, height: size, fontSize: Math.round(size * 0.42) }}
      aria-hidden
    >
      {app.logoUrl ? <img src={app.logoUrl} alt="" /> : initial}
    </span>
  );
}

export function ApplicationHeader({
  app,
  eyebrow = "OAuth application",
  actions,
}: {
  app: OAuthApplication;
  eyebrow?: string;
  actions?: React.ReactNode;
}) {
  return (
    <header className="application-header">
      <OAuthAppImage app={app} size={48} />
      <div className="application-header-copy">
        <p className="application-header-eyebrow">
          <AppWindow size={12} />
          {eyebrow}
        </p>
        <h1 data-i18n-ignore>{app.name}</h1>
        {app.description ? (
          <p className="application-header-description" data-i18n-ignore>
            {app.description}
          </p>
        ) : (
          <p className="application-header-description">
            Client ID <code data-i18n-ignore>{app.clientId}</code>
          </p>
        )}
      </div>
      {actions ? <div className="application-header-actions">{actions}</div> : null}
    </header>
  );
}
