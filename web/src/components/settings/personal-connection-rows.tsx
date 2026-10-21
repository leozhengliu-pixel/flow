import { ChevronDown } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";

import { confirmAction } from "@/components/ui/action-dialog-service";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { useI18n } from "@/i18n/i18n";
import {
  authorizeGoogleCalendar,
  connectNotion,
  disconnectGoogleCalendar,
  disconnectNotion,
  fetchPersonalConnections,
} from "@/lib/personal-connections-api";
import { toast } from "@/lib/toast";
import type { PersonalConnection, PersonalConnectionOption } from "@/types/personal-connections";

import { ConnectAction } from "./code-host-accounts";
import { IntegrationBrandIcon } from "./integration-brand-icon";
import { SettingsRow } from "./settings-primitives";

import "./code-host-accounts.css";

const GOOGLE_DESCRIPTION = "Sync your calendar out-of-office status with Flow";
const NOTION_DESCRIPTION = "Preview issues, projects, and views within Notion";
const NOTION_ENABLED_DESCRIPTION = "Previews are on. Authorize Flow in Notion to see them there.";

/** Loads the viewer's Google Calendar and Notion connections. */
function usePersonalConnections() {
  const { t } = useI18n();
  const [data, setData] = useState<{ googleCalendar: PersonalConnectionOption; notion: PersonalConnectionOption } | null>(null);
  const reload = useCallback(async () => {
    try {
      setData(await fetchPersonalConnections());
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not load connected accounts"));
    }
  }, [t]);
  useEffect(() => {
    void reload();
    // Load once per mount; `t` only changes with the locale.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return { data, reload };
}

/** Shows the toast for the `personalConnection` result the server appends after Google, then strips it from the URL. */
function usePersonalConnectionResult() {
  const { t } = useI18n();
  const handled = useRef(false);
  useEffect(() => {
    if (handled.current || typeof window === "undefined") return;
    const params = new URLSearchParams(window.location.search);
    const result = params.get("personalConnection");
    if (!result) return;
    handled.current = true;
    if (result === "linked") toast.success(t("Account connected"));
    else toast.error(params.get("error") || t("Could not connect the account"));
    params.delete("personalConnection");
    params.delete("error");
    const query = params.toString();
    window.history.replaceState(
      window.history.state,
      "",
      `${window.location.pathname}${query ? `?${query}` : ""}${window.location.hash}`,
    );
  }, [t]);
}

/** "● Connected ⌄" pill whose menu reconnects or disconnects. */
function ConnectionStatusMenu({
  name,
  reconnect,
  connectedLabel,
  manageLabel,
  disabled,
  onReconnect,
  onManage,
  onDisconnect,
}: {
  name: string;
  reconnect: boolean;
  connectedLabel?: string;
  manageLabel?: string;
  onManage?: () => void;
  disabled?: boolean;
  onReconnect?: () => void;
  onDisconnect: () => void;
}) {
  const { t } = useI18n();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className={`personal-action code-host-status${reconnect ? " reconnect" : ""}`}
          aria-label={t("{name} account options").replace("{name}", name)}
          disabled={disabled}
        >
          <span className="code-host-status-dot" aria-hidden="true" />
          {reconnect ? t("Reconnect needed") : (connectedLabel ?? t("Connected"))}
          <ChevronDown aria-hidden="true" className="code-host-status-chevron" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {onManage && manageLabel && <DropdownMenuItem onSelect={onManage}>{manageLabel}</DropdownMenuItem>}
        {reconnect && onReconnect && <DropdownMenuItem onSelect={onReconnect}>{t("Reconnect")}</DropdownMenuItem>}
        <DropdownMenuItem onSelect={onDisconnect}>{t("Disconnect…")}</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function ConnectionRow({
  provider,
  name,
  description,
  option,
  loaded,
  busy,
  unavailableHint,
  connectedLabel,
  manageLabel,
  onConnect,
  onManage,
  onDisconnect,
}: {
  provider: "google-calendar" | "notion";
  name: string;
  description: ReactNode;
  option: PersonalConnectionOption | undefined;
  loaded: boolean;
  busy: boolean;
  unavailableHint: string;
  connectedLabel?: string;
  manageLabel?: string;
  onManage?: () => void;
  onConnect: () => void;
  onDisconnect: (connection: PersonalConnection) => void;
}) {
  const { t } = useI18n();
  const connection = option?.connection;
  const reconnect = connection?.status === "reconnect";
  let control: ReactNode = null;
  if (connection) {
    control = (
      <ConnectionStatusMenu
        name={name}
        reconnect={reconnect}
        connectedLabel={connectedLabel}
        manageLabel={manageLabel}
        onManage={onManage}
        disabled={busy}
        onReconnect={onConnect}
        onDisconnect={() => onDisconnect(connection)}
      />
    );
  } else if (option?.available) {
    control = <ConnectAction disabled={busy} onClick={onConnect} />;
  } else if (loaded) {
    control = (
      <span className="personal-connection-soon" title={unavailableHint}>
        {t("Not configured")}
      </span>
    );
  }
  return (
    <div className="settings-card" data-testid={`${provider}-connection-row`}>
      <SettingsRow
        icon={<IntegrationBrandIcon provider={provider} size={16} />}
        title={<span data-i18n-ignore>{name}</span>}
        description={description}
      >
        {control}
      </SettingsRow>
    </div>
  );
}

/** The Google Calendar and Notion rows of Settings › Account › Connected accounts. */
export function PersonalConnectionRows() {
  const { t, formatDate } = useI18n();
  const { data, reload } = usePersonalConnections();
  usePersonalConnectionResult();
  const [busy, setBusy] = useState(false);

  const run = async (action: () => Promise<void>, failure: string) => {
    setBusy(true);
    try {
      await action();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : failure);
      setBusy(false);
    }
  };
  const redirectTo = async (start: () => Promise<{ url: string }>) => {
    await run(async () => {
      const { url } = await start();
      window.location.assign(url);
    }, t("Could not start the connection"));
  };
  const disconnect = async (provider: "google-calendar" | "notion") => {
    const name = provider === "google-calendar" ? "Google Calendar" : "Notion";
    const confirmed = await confirmAction(t("Disconnect {name}?").replace("{name}", name), {
      confirmLabel: t("Disconnect"),
      danger: true,
    });
    if (!confirmed) return;
    await run(async () => {
      await (provider === "google-calendar" ? disconnectGoogleCalendar() : disconnectNotion());
      await reload();
      setBusy(false);
    }, t("Could not disconnect the account"));
  };

  const google = data?.googleCalendar;
  const googleConnection = google?.connection;
  // Flow cannot see Notion's side of the authorization, so the Notion row
  // only reports that previews are on and offers a way back to Notion.
  const manageNotion = () =>
    void run(async () => {
      const { url } = await connectNotion();
      window.open(url, "_blank", "noopener,noreferrer");
      setBusy(false);
    }, t("Could not start the connection"));
  const away = googleConnection?.status === "active" && googleConnection.awayUntil && new Date(googleConnection.awayUntil) > new Date();
  let googleDescription: ReactNode = t(GOOGLE_DESCRIPTION);
  if (googleConnection?.status === "reconnect" && googleConnection.statusReason) {
    googleDescription = <span data-i18n-ignore>{googleConnection.statusReason}</span>;
  } else if (googleConnection) {
    googleDescription = (
      <>
        {away ? `${t("Out of office until")} ` : `${t(GOOGLE_DESCRIPTION)} · `}
        <span data-i18n-ignore>{away ? formatDate(googleConnection.awayUntil as string, { month: "short", day: "numeric" }) : googleConnection.account}</span>
      </>
    );
  }

  return (
    <>
      <ConnectionRow
        provider="google-calendar"
        name="Google Calendar"
        description={googleDescription}
        option={google}
        loaded={data !== null}
        busy={busy}
        unavailableHint={t("An operator must set the Google OAuth client for this deployment.")}
        onConnect={() => void redirectTo(authorizeGoogleCalendar)}
        onDisconnect={() => void disconnect("google-calendar")}
      />
      <ConnectionRow
        provider="notion"
        name="Notion"
        description={t(data?.notion?.connection ? NOTION_ENABLED_DESCRIPTION : NOTION_DESCRIPTION)}
        option={data?.notion}
        connectedLabel={t("Previews on")}
        manageLabel={t("Manage in Notion")}
        onManage={manageNotion}
        loaded={data !== null}
        busy={busy}
        unavailableHint={t("An operator must set the Notion connection URL for this deployment.")}
        onConnect={() => void redirectTo(connectNotion)}
        onDisconnect={() => void disconnect("notion")}
      />
    </>
  );
}
