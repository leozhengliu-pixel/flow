import {
  Children,
  useEffect,
  useMemo,
  useState,
  type ComponentProps,
  type ReactNode,
} from "react";
import {
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Mail,
  MessageSquare,
  Plus,
  Search,
  type LucideIcon,
} from "lucide-react";
import { toast } from "sonner";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useI18n } from "@/i18n/i18n";
import {
  authorizeIntegration,
  disconnectIntegration,
  updateWorkspacePreferences,
} from "@/lib/api";
import type {
  AsksSlackChannelMapping,
  BootstrapData,
  FeatureSettings,
  WorkspaceSettings,
} from "@/types/flow";
import { SettingsToggle as BaseSettingsToggle } from "./settings-primitives";
import { intakeDescription, intakeTitle } from "./email-intake-format";

import "./feature-settings.css";
import "./asks-settings.css";

type SaveFeature = <K extends keyof FeatureSettings>(
  key: K,
  value: FeatureSettings[K],
) => void;

const DEFAULT_ASKS_CHANNELS: AsksSlackChannelMapping[] = [];

function message(error: unknown) {
  return error instanceof Error ? error.message : "Could not update Asks settings";
}

function asksSlack(data: BootstrapData) {
  const connected = data.integrationConnections.find(
    (item) =>
      item.provider === "slack" &&
      item.status === "connected" &&
      (item.config?.scope === "asks" || !item.config?.scope),
  );
  if (connected) return connected;
  return data.integrationConnections.find(
    (item) => item.provider === "slack" && item.status === "connected",
  );
}

export function AsksSettingsPage({
  data,
  settings,
  busy,
  setEnabled,
  setFeature,
  onReload,
  onOpenSlack,
  onOpenEmailIntake,
}: {
  data: BootstrapData;
  settings: WorkspaceSettings;
  busy: boolean;
  setEnabled: (id: string, value: boolean) => void;
  setFeature: SaveFeature;
  onReload: () => Promise<void>;
  onOpenSlack?: (integrationId: string) => void;
  /** Opens the email intake wizard, or an address detail page when given an id. */
  onOpenEmailIntake?: (addressId?: string) => void;
}) {
  const { t } = useI18n();
  const slack = asksSlack(data);
  const emails = settings.featureSettings.asksEmailAddresses ?? [];
  // Asks addresses, plus legacy custom-domain addresses opted in by address.
  const emailIntakes = data.emailIntakeAddresses
    .filter((item) => item.enabled && (item.type === "asks" || emails.includes(item.address)))
    .sort((a, b) => intakeTitle(a).localeCompare(intakeTitle(b)));
  const legacyEmails = emails.filter((email) => !data.emailIntakeAddresses.some((item) => item.enabled && item.address === email));

  const toggleSlack = async () => {
    if (busy) return;
    try {
      if (slack) await disconnectIntegration("slack");
      else
        await authorizeIntegration(
          "slack",
          { name: "Slack", config: { scope: "asks" } },
          Boolean(
            data.integrationConnections.find((item) => item.provider === "slack"),
          ),
        );
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
  };

  return (
    <FeatureShell
      title="Asks"
      description="Let anyone submit bug reports, feature requests, and more using structured templates from Slack or email."
    >
      <FeatureCard>
        <FeatureRow
          title="Enable Asks"
          description="Allow members to create issues through Asks"
        >
          <Toggle
            checked={settings.featureFlags.asks ?? true}
            disabled={busy}
            label="Enable Asks"
            onChange={(value) => setEnabled("asks", value)}
          />
        </FeatureRow>
      </FeatureCard>

      <FeatureSection
        title="Slack"
        description="Allow anyone in your Slack workspace to submit Asks using templated forms"
      >
        {slack ? (
          <FeatureCard>
            <button
              type="button"
              className="asks-clickable-row"
              disabled={busy || !onOpenSlack}
              onClick={() => onOpenSlack?.(slack.id)}
            >
              <FeatureRow
                icon={MessageSquare}
                title={slack.name}
                businessTitle
                description="Connected workspace · manage channels and templates"
              >
                <ChevronRight size={15} />
              </FeatureRow>
            </button>
            <FeatureRow title="Connection">
              <FeatureButton danger disabled={busy} onClick={() => void toggleSlack()}>
                Disconnect
              </FeatureButton>
            </FeatureRow>
          </FeatureCard>
        ) : (
          <FeatureEmpty
            icon={MessageSquare}
            title="No workspaces connected"
            action={
              <FeatureButton
                aria-label={t("Connect workspace")}
                disabled={busy}
                onClick={() => void toggleSlack()}
              >
                <Plus size={14} />
              </FeatureButton>
            }
          />
        )}
      </FeatureSection>

      <FeatureSection
        title="Email"
        description="Allow anyone to submit Asks by emailing a custom address"
      >
        <div className="feature-card asks-email-list">
          <div className="asks-email-list-header">
            <span className={emailIntakes.length || legacyEmails.length ? "" : "is-muted"}>
              {emailIntakes.length + legacyEmails.length
                ? (emailIntakes.length + legacyEmails.length === 1 ? t("1 email") : t("{count} emails").replace("{count}", String(emailIntakes.length + legacyEmails.length)))
                : t("No email addresses configured")}
            </span>
            <button
              type="button"
              aria-label={t("Add email")}
              title={t("Add email")}
              className="asks-email-add"
              disabled={busy || !onOpenEmailIntake}
              onClick={() => onOpenEmailIntake?.()}
            >
              <Plus size={14} />
            </button>
          </div>
          {emailIntakes.map((intake) => (
            <button
              type="button"
              key={intake.id}
              className="asks-email-list-row"
              aria-label={t("{name} settings").replace("{name}", intakeTitle(intake))}
              onClick={() => onOpenEmailIntake?.(intake.id)}
            >
              <Mail size={16} />
              <span>
                <strong data-i18n-ignore>{intakeTitle(intake)}</strong>
                {intakeDescription(intake) && <small data-i18n-ignore>{intakeDescription(intake)}</small>}
              </span>
              <ChevronRight size={14} />
            </button>
          ))}
          {legacyEmails.map((email) => (
            <div key={email} className="asks-email-list-row">
              <Mail size={16} />
              <span><strong data-i18n-ignore>{email}</strong></span>
              <FeatureButton
                danger
                disabled={busy}
                onClick={() => setFeature("asksEmailAddresses", emails.filter((value) => value !== email))}
              >
                Remove
              </FeatureButton>
            </div>
          ))}
        </div>
      </FeatureSection>
    </FeatureShell>
  );
}

export function AsksSlackSettingsPage({
  data,
  integrationId,
  onBack,
  onReload,
}: {
  data: BootstrapData;
  integrationId: string;
  onBack: () => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [settings, setSettings] = useState(data.workspaceSettings);
  const connection =
    data.integrationConnections.find((item) => item.id === integrationId) ??
    asksSlack(data);
  const mappings = settings.featureSettings?.asksSlackChannels ?? DEFAULT_ASKS_CHANNELS;
  useEffect(() => setSettings(data.workspaceSettings), [data.workspaceSettings]);
  const setFeature = async <K extends keyof FeatureSettings>(
    key: K,
    value: FeatureSettings[K],
  ) => {
    if (busy || !["admin", "owner"].includes(data.viewerRole)) return;
    const before = settings;
    const next = {
      ...settings,
      featureSettings: { ...settings.featureSettings, [key]: value },
    };
    setSettings(next);
    setBusy(true);
    try {
      const updated = await updateWorkspacePreferences(
        { featureSettings: { [key]: value } },
        data.workspace.urlKey,
      );
      setSettings(updated);
    } catch (error) {
      setSettings(before);
      toast.error(message(error));
    } finally {
      setBusy(false);
    }
  };
  const channels = useMemo(() => {
    const listed = connection?.channels ?? [];
    const extras = mappings.map((item) => item.channel).filter(Boolean);
    return [...new Set([...listed, ...extras])].sort((a, b) =>
      a.localeCompare(b),
    );
  }, [connection?.channels, mappings]);
  const filtered = channels.filter((channel) =>
    channel.toLowerCase().includes(query.trim().toLowerCase()),
  );
  const teams = data.teams.filter((team) => !team.retiredAt);
  const templates = data.issueTemplates ?? [];

  if (!connection || connection.status !== "connected") {
    return (
      <FeatureShell title="Asks · Slack">
        <button type="button" className="asks-settings-back" onClick={onBack}>
          <ChevronLeft size={14} />
          {t("Back to Asks")}
        </button>
        <FeatureEmpty
          icon={MessageSquare}
          title="No workspaces connected"
          action={
            <FeatureButton onClick={onBack}>Back to Asks</FeatureButton>
          }
        />
      </FeatureShell>
    );
  }

  const upsert = (channel: string, patch: Partial<AsksSlackChannelMapping>) => {
    const existing = mappings.find((item) => item.channel === channel);
    const next: AsksSlackChannelMapping = {
      channel,
      teamId: patch.teamId ?? existing?.teamId ?? teams[0]?.id ?? "",
      templateId: patch.templateId ?? existing?.templateId,
      enabled: patch.enabled ?? existing?.enabled ?? true,
    };
    if (!next.teamId) {
      toast.error(t("Select a team before enabling a channel"));
      return;
    }
    const without = mappings.filter((item) => item.channel !== channel);
    void setFeature("asksSlackChannels", [...without, next]);
  };

  const remove = (channel: string) => {
    void setFeature(
      "asksSlackChannels",
      mappings.filter((item) => item.channel !== channel),
    );
  };

  const addChannel = () => {
    const name = window.prompt(t("Slack channel name (without #)"));
    if (!name?.trim()) return;
    const channel = name.trim().replace(/^#/, "");
    if (channels.includes(channel)) {
      toast.error(t("Channel already listed"));
      return;
    }
    if (!teams[0]) {
      toast.error(t("Create a team before mapping Slack channels"));
      return;
    }
    upsert(channel, { teamId: teams[0].id, enabled: true });
  };

  const turnOff = async () => {
    if (busy) return;
    try {
      await disconnectIntegration("slack");
      await onReload();
      onBack();
    } catch (error) {
      toast.error(message(error));
    }
  };

  return (
    <FeatureShell
      title="Asks · Slack"
      description="Map Slack channels to teams and templates so Asks can create issues in the right place."
    >
      <button type="button" className="asks-settings-back" onClick={onBack}>
        <ChevronLeft size={14} />
        {t("Back to Asks")}
      </button>

      <FeatureCard>
        <FeatureRow
          icon={MessageSquare}
          title={connection.name}
          businessTitle
          description="Connected Slack workspace"
        >
          <FeatureButton danger disabled={busy} onClick={() => void turnOff()}>
            Turn off Asks
          </FeatureButton>
        </FeatureRow>
      </FeatureCard>

      <FeatureSection
        title="Connected Slack channels"
        description="Choose which channels can submit Asks, and which team and template they use"
      >
        <div className="asks-slack-toolbar">
          <label>
            <Search size={14} />
            <input
              type="search"
              aria-label={t("Filter channels")}
              placeholder={t("Filter channels…")}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </label>
          <FeatureButton disabled={busy} onClick={addChannel}>
            <Plus size={14} />
            Add channel
          </FeatureButton>
        </div>

        {filtered.length ? (
          <FeatureCard>
            {filtered.map((channel) => {
              const mapping = mappings.find((item) => item.channel === channel);
              const teamTemplates = templates.filter(
                (item) =>
                  !item.teamId ||
                  item.teamId === mapping?.teamId ||
                  item.scope === "workspace",
              );
              return (
                <div className="asks-channel-row" key={channel}>
                  <strong data-i18n-ignore>#{channel}</strong>
                  <FeatureSelect
                    label={`Team for ${channel}`}
                    value={mapping?.teamId ?? ""}
                    disabled={busy}
                    options={[
                      { value: "", label: "Select team" },
                      ...teams.map((team) => ({
                        value: team.id,
                        label: team.name,
                        translate: false as const,
                      })),
                    ]}
                    onChange={(teamId) => {
                      if (!teamId) {
                        remove(channel);
                        return;
                      }
                      upsert(channel, { teamId, templateId: undefined });
                    }}
                  />
                  <FeatureSelect
                    label={`Template for ${channel}`}
                    value={mapping?.templateId ?? ""}
                    disabled={busy || !mapping?.teamId}
                    options={[
                      { value: "", label: "No template" },
                      ...teamTemplates.map((item) => ({
                        value: item.id,
                        label: item.name,
                        translate: false as const,
                      })),
                    ]}
                    onChange={(templateId) =>
                      upsert(channel, {
                        teamId: mapping?.teamId,
                        templateId: templateId || undefined,
                      })
                    }
                  />
                  <FeatureButton
                    danger
                    disabled={busy || !mapping}
                    onClick={() => remove(channel)}
                  >
                    Remove
                  </FeatureButton>
                </div>
              );
            })}
          </FeatureCard>
        ) : (
          <FeatureEmpty
            icon={MessageSquare}
            title={
              query
                ? "No channels match the current filter"
                : "No channels configured"
            }
            action={
              !query ? (
                <FeatureButton disabled={busy} onClick={addChannel}>
                  <Plus size={14} />
                  Add channel
                </FeatureButton>
              ) : undefined
            }
          />
        )}
      </FeatureSection>

      <FeatureSection title="Templates">
        <FeatureCard>
          <FeatureRow
            title="Channel templates"
            description={
              mappings.filter((item) => item.templateId).length
                ? `${mappings.filter((item) => item.templateId).length} templates linked`
                : "No templates"
            }
          >
            <span className="feature-state">
              {mappings.length
                ? t("{count} channels mapped").replace(
                    "{count}",
                    String(mappings.length),
                  )
                : t("None")}
            </span>
          </FeatureRow>
        </FeatureCard>
      </FeatureSection>

      <FeatureSection
        title="Unfurls, agent, and workflows"
        description="Advanced Slack Asks behaviors"
      >
        <div className="asks-coming-soon">
          <strong>{t("Coming soon")}</strong>
          <span>
            {t(
              "Unfurls, work objects, issue updates in Slack, Asks agent, and Slack workflows are not available yet.",
            )}
          </span>
        </div>
      </FeatureSection>
    </FeatureShell>
  );
}

export { AsksEmailIntakeDetailPage, NewAsksEmailIntakePage } from "./asks-email-intake";

function FeatureShell({
  className,
  title,
  description,
  children,
}: {
  className?: string;
  title: string;
  description?: ReactNode;
  children: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <div className={`feature-settings${className ? ` ${className}` : ""}`}>
      <header className="feature-header">
        <h1>{t(title)}</h1>
        {description && (
          <p>{typeof description === "string" ? t(description) : description}</p>
        )}
      </header>
      {children}
    </div>
  );
}
function FeatureSection({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <section className="feature-section">
      <header>
        <h2>{t(title)}</h2>
        {description && <p>{t(description)}</p>}
      </header>
      {children}
    </section>
  );
}
function FeatureCard({ children }: { children: ReactNode }) {
  return <div className="feature-card">{children}</div>;
}
function Toggle(props: ComponentProps<typeof BaseSettingsToggle>) {
  const { t } = useI18n();
  return <BaseSettingsToggle {...props} label={t(props.label)} />;
}
function FeatureRow({
  title,
  description,
  icon: Icon,
  businessTitle,
  children,
}: {
  title: string;
  description?: string;
  icon?: LucideIcon;
  businessTitle?: boolean;
  children?: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <div className="feature-row">
      {Icon && (
        <span className="feature-row-icon">
          <Icon size={18} />
        </span>
      )}
      <div>
        <strong data-i18n-ignore={businessTitle || undefined}>
          {businessTitle ? title : t(title)}
        </strong>
        {description && <span>{t(description)}</span>}
      </div>
      {children && <aside>{children}</aside>}
    </div>
  );
}
function FeatureButton({
  children,
  primary,
  danger,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  primary?: boolean;
  danger?: boolean;
}) {
  const { t } = useI18n();
  return (
    <button
      {...props}
      className={`feature-button${primary ? " primary" : ""}${danger ? " danger" : ""}`}
    >
      {Children.map(children, (child) =>
        typeof child === "string" ? t(child) : child,
      )}
    </button>
  );
}
function FeatureSelect({
  label,
  value,
  options,
  onChange,
  disabled,
}: {
  label: string;
  value: string;
  options: { value: string; label: string; translate?: boolean }[];
  onChange: (value: string) => void;
  disabled?: boolean;
}) {
  const { t } = useI18n();
  const selected = options.find((item) => item.value === value) ?? {
    value,
    label: value,
  };
  const text = (item: { label: string; translate?: boolean }) =>
    item.translate === false ? item.label : t(item.label);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          role="combobox"
          aria-label={t(label)}
          disabled={disabled}
          className="feature-select"
        >
          <span data-i18n-ignore={selected.translate === false ? true : undefined}>
            {text(selected)}
          </span>
          <ChevronDown size={13} />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="feature-select-menu">
        {options.map((item) => (
          <DropdownMenuItem
            key={item.value}
            data-i18n-ignore={item.translate === false ? true : undefined}
            onSelect={() => onChange(item.value)}
          >
            {text(item)}
            {item.value === value && <Check size={13} />}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
function FeatureEmpty({
  icon: Icon,
  title,
  action,
}: {
  icon: LucideIcon;
  title: string;
  action?: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <div className="feature-empty">
      <Icon size={24} />
      <h3>{t(title)}</h3>
      {action}
    </div>
  );
}
