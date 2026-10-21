import { useEffect, useState, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowRight, ArrowUpRight, Box, ChevronRight, Copy, Eye, EyeOff, Check, Link2, MessageSquare, SquareUser, Tag, TrendingUp, Users } from "lucide-react";

import { ApplicationMembers } from "@/components/agent/application-members";
import { AgentCursorGlyph, CodeIntelligenceGlyph } from "@/components/ui/agent-glyph";
import { FlowGlyph } from "@/components/ui/menu-glyphs";
import { useI18n } from "@/i18n/i18n";
import { listApplications, type ApplicationInstallation } from "@/lib/application-agents";
import { agentWebAccessEnabled } from "@/lib/agent-web-access";
import { settingsPath, type SettingsPageId } from "@/lib/app-routes";
import { updateWorkspaceAgentGuidance, updateIntegrationConnection, type updateWorkspacePreferences } from "@/lib/api";
import { DEFAULT_REPOSITORY_ACCESS, githubConnection, type RepositoryAccessSettings } from "@/lib/code-access";
import { toast } from "@/lib/toast";
import type { BootstrapData, FeatureSettings, WorkspaceSettings } from "@/types/flow";
import { CodingSessionsAIRow } from "./coding-sessions/coding-sessions-settings";
import { CodeIntelligencePage } from "./code-intelligence-settings";
import { FeatureCard, FeatureLinkRow, FeatureRow, FeatureSection, FeatureSelect, FeatureShell, Toggle, type FeatureIcon } from "./feature-primitives";

import "./ai-settings-pages.css";

export type AiPageProps = {
  data: BootstrapData;
  settings: WorkspaceSettings;
  /** Not an admin, or a workspace save is in flight. */
  busy: boolean;
  setEnabled: (id: string, value: boolean) => void;
  setFeature: <K extends keyof FeatureSettings>(key: K, value: FeatureSettings[K]) => void;
  save: (next: Parameters<typeof updateWorkspacePreferences>[0]) => Promise<void>;
  onReload: () => Promise<void>;
  onNavigateSettings?: (page: SettingsPageId) => void;
  onOpenIntegration: (provider: string) => void;
};

const AI_BACK = { label: "AI & agents" };
const DOCS_AGENT = "https://flow.app/docs/flow-agent";
const DOCS_AGENTS = "https://flow.app/docs/agents";
const DOCS_TRIAGE = "https://flow.app/docs/triage-intelligence";

function message(error: unknown) {
  return error instanceof Error ? error.message : "Could not update feature settings";
}

function DocsLink({ href }: { href: string }) {
  const { t } = useI18n();
  return (
    <a className="feature-docs-link" href={href} rel="noreferrer" target="_blank">
      {t("Docs")}
      <ArrowUpRight size={11} />
    </a>
  );
}

const status = (enabled: boolean) => (enabled ? "Enabled" : "Disabled");

/** Settings › AI & agents: usage, usage feedback, then a link row for each AI feature. */
export function AiSettingsHub({ data, settings, busy, setEnabled, onNavigateSettings }: AiPageProps) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const go = (page: SettingsPageId) => () => onNavigateSettings?.(page);
  const flag = (id: string, fallback: boolean) => settings.featureFlags[id] ?? fallback;
  const workspace = data.workspace.urlKey;
  const [applications, setApplications] = useState<ApplicationInstallation[]>([]);
  const isAdmin = ["admin", "owner"].includes(data.viewerRole);
  useEffect(() => {
    if (!isAdmin) return;
    let active = true;
    void listApplications(workspace).then((items) => active && setApplications(items.filter((item) => item.active))).catch(() => undefined);
    return () => {
      active = false;
    };
  }, [workspace, isAdmin]);
  return (
    <FeatureShell className="feature-ai-settings" title="AI & agents" description="Automate your product development processes and operations with AI">
      <div className="feature-card-stack">
        <FeatureCard>
          <FeatureLinkRow title="Usage" description="$0.00 remaining" onClick={go("usage")} />
        </FeatureCard>
        <FeatureCard>
          <FeatureRow title="Enable usage feedback" description="Improve AI functionality by sharing usage feedback. Never used to train models">
            <Toggle checked={flag("usage-feedback", true)} disabled={busy} label="Enable usage feedback" onChange={(value) => setEnabled("usage-feedback", value)} />
          </FeatureRow>
        </FeatureCard>
      </div>
      <FeatureSection title="Flow Agent" description={<>{t("Create issues and answer questions about your workspace.")} <DocsLink href={DOCS_AGENT} /></>}>
        <FeatureCard>
          <FeatureLinkRow icon={AgentCursorGlyph} title="Flow Agent" description="Configure for your workspace" detail={status(flag("ai-agent", true))} onClick={go("ai-agent")} />
          <CodingSessionsAIRow enabled={flag("coding-sessions", true)} onOpen={go("coding-sessions")} />
          <FeatureLinkRow icon={CodeIntelligenceGlyph} title="Code Intelligence" description="Allow Flow Agent and Loops to analyze and answer questions about your code" detail={status(flag("code-intelligence", false))} onClick={go("code-intelligence")} />
          <FeatureLinkRow icon={TriageGlyph} title="Triage Intelligence" description="Find related issues and infer properties like team, project, labels, and assignee" detail={status(flag("triage-intelligence", false))} onClick={go("triage-intelligence")} />
        </FeatureCard>
      </FeatureSection>
      <FeatureSection title="Flow Agent integrations" description="Integrations available to Flow Agent.">
        <FeatureCard>
          <FeatureLinkRow title="Available integrations" description="Available on Slack and Asks for Slack. Add integrations to your workspace to use." onClick={go("integrations")} />
        </FeatureCard>
      </FeatureSection>
      <FeatureSection title="Installed Agents" description={<>{t("AI agents can work alongside you as teammates.")} <DocsLink href={DOCS_AGENTS} /></>}>
        <div className="feature-card-stack">
          {applications.map((item) => (
            <FeatureCard key={item.id}>
              <button type="button" className="feature-row feature-row-link" onClick={() => navigate(`${settingsPath(workspace, "applications")}/${encodeURIComponent(item.clientId)}`)}>
                <span className="feature-row-icon feature-agent-avatar" aria-hidden="true" data-i18n-ignore>{initials(item.name)}</span>
                <div>
                  <strong data-i18n-ignore>{item.name}</strong>
                  <span>{item.allPublicTeams ? t("Access to all public teams") : t("Access to {count} teams").replace("{count}", String(item.teamIds.length))}</span>
                </div>
                <aside><ChevronRight size={15} aria-hidden="true" /></aside>
              </button>
            </FeatureCard>
          ))}
          <FeatureCard>
            <FeatureLinkRow icon={AgentCursorGlyph} title="Available integrations" description="Browse agents you can install in your workspace" onClick={go("integrations")} />
          </FeatureCard>
          <FeatureCard>
            <FeatureLinkRow icon={AgentCursorGlyph} title="Installed agents guidance" description="Provide context and instructions for installed agents" onClick={go("installed-agents")} />
          </FeatureCard>
        </div>
      </FeatureSection>
      <FeatureSection title="AI" description="Automate your product development processes and operations with AI">
        <FeatureCard>
          <FeatureLinkRow icon={MessageSquare} title="Resolved thread summaries" description="Control AI summaries for resolved threads across Flow" detail={status(flag("thread-summaries", true))} onClick={go("ai-summaries")} />
          <FeatureRow title="Suggest branch name in prompts" description="Include a branch name in the default prompt when members copy an issue as a prompt or open it in a coding tool">
            <Toggle checked={flag("suggest-branch-name", true)} disabled={busy} label="Suggest branch name in prompts" onChange={(value) => setEnabled("suggest-branch-name", value)} />
          </FeatureRow>
        </FeatureCard>
      </FeatureSection>
    </FeatureShell>
  );
}

const initials = (name: string) => name.split(/\s+/).filter(Boolean).slice(0, 2).map((part) => part[0]?.toUpperCase()).join("");
const TriageGlyph: FeatureIcon = ({ size = 16 }) => <FlowGlyph name="triage" size={Number(size) || 16} />;

/** A borderless-looking multi-line field that saves when it loses focus. */
function GuidanceField({ label, value, disabled, placeholder = "Optional agent guidance...", onSave }: { label: string; value: string; disabled?: boolean; placeholder?: string; onSave: (value: string) => Promise<void> | void }) {
  const { t } = useI18n();
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  return (
    <textarea
      className="feature-guidance-field"
      aria-label={t(label)}
      placeholder={t(placeholder)}
      maxLength={8000}
      disabled={disabled}
      value={draft}
      onChange={(event) => setDraft(event.target.value)}
      onBlur={() => {
        const next = draft.trim();
        if (next !== value.trim()) void onSave(next);
      }}
    />
  );
}

/** Settings › AI & agents › Flow Agent. */
export function AiAgentSettings({ data, settings, busy, setEnabled, save, onReload, onNavigateSettings }: AiPageProps) {
  const { t } = useI18n();
  const canEditGuidance = ["owner", "admin"].includes(data.viewerRole) || settings.agentGuidancePermission === "members";
  const webAccess = agentWebAccessEnabled(settings);
  const saveGuidance = async (value: string) => {
    try {
      await updateWorkspaceAgentGuidance(value);
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
  };
  return (
    <FeatureShell className="feature-ai-settings" title="Flow Agent" description="Create issues and answer questions about your workspace" back={{ ...AI_BACK, onClick: () => onNavigateSettings?.("ai") }}>
      <FeatureCard>
        <FeatureRow title="Enable Flow Agent" description="Allow conversations with Flow Agent inside your workspace">
          <Toggle checked={settings.featureFlags["ai-agent"] ?? true} disabled={busy} label="Enable Flow Agent" onChange={(value) => setEnabled("ai-agent", value)} />
        </FeatureRow>
        <FeatureRow title="Enable web access" description="Allow this agent to access the public web for current information (workspace content may be sent to external services)">
          <Toggle checked={webAccess} disabled={busy} label="Enable web access" onChange={(value) => void save({ agentWebAccess: value, agentWebSearch: value, agentWebSearchDisabled: !value })} />
        </FeatureRow>
      </FeatureCard>
      <FeatureSection
        title="MCP connectors"
        description={<>{t("Allow Flow Agent to use MCP connectors added by workspace members. Add personal MCP connectors in")}{" "}<button type="button" className="feature-section-link" onClick={() => onNavigateSettings?.("agents")}>{t("Agent personalization")}</button></>}
      >
        <FeatureCard>
          <FeatureRow title="Enable MCP connectors" description="Allow Flow Agent to use MCP connectors.">
            <Toggle checked={Boolean(settings.mcpConnectorsEnabled)} disabled={busy} label="Enable MCP connectors" onChange={(value) => void save({ mcpConnectorsEnabled: value })} />
          </FeatureRow>
          <FeatureRow title="Allowed MCP connectors" description="Configure which MCP connectors can be used in this workspace">
            <button type="button" className="feature-section-link is-action" onClick={() => onNavigateSettings?.("security")}>
              {t("Manage")}
              <ArrowRight size={12} />
            </button>
          </FeatureRow>
        </FeatureCard>
      </FeatureSection>
      <FeatureSection title="Workspace guidance" description="Provide instructions and context for Flow Agent when responding to conversations within Flow">
        <div className="feature-card-stack">
          <FeatureCard>
            <FeatureLinkRow icon={TrendingUp} title="Updates" description="Customize how project and initiative updates should be written" onClick={() => onNavigateSettings?.("ai-updates")} />
          </FeatureCard>
          <GuidanceField label="Workspace guidance" value={settings.agentInstructions ?? ""} disabled={!canEditGuidance} onSave={saveGuidance} />
        </div>
      </FeatureSection>
    </FeatureShell>
  );
}

const TRIAGE_ROWS: Array<[keyof FeatureSettings["triageIntelligence"], string, FeatureIcon, boolean]> = [
  ["assigneeAction", "Assignee is suggested", Users, true],
  ["projectAction", "Project is suggested", Box, true],
  ["labelAction", "Label is suggested", Tag, true],
  ["teamAction", "Team is suggested", SquareUser, true],
  ["duplicateAction", "Duplicate issue is suggested", Copy, false],
  ["relatedAction", "Related issue is suggested", Link2, false],
];

/** Settings › AI & agents › Triage Intelligence. */
export function TriageIntelligenceSettings({ settings, busy, setEnabled, setFeature, onNavigateSettings }: AiPageProps) {
  const { t } = useI18n();
  const config = settings.featureSettings.triageIntelligence;
  const enabled = settings.featureFlags["triage-intelligence"] ?? false;
  const update = <K extends keyof typeof config>(key: K, value: (typeof config)[K]) => setFeature("triageIntelligence", { ...config, [key]: value });
  const options = [
    { value: "suggest", label: "Show", icon: <Eye size={14} /> },
    { value: "auto", label: "Auto-apply", icon: <Check size={14} /> },
    { value: "hide", label: "Hide", icon: <EyeOff size={14} /> },
  ];
  return (
    <FeatureShell
      className="feature-ai-settings"
      title="Triage Intelligence"
      description={<>{t("Flow uses agentic AI to automatically find related issues & duplicates, and infer properties like team, project, labels, and assignee.")} <DocsLink href={DOCS_TRIAGE} /></>}
      back={{ ...AI_BACK, onClick: () => onNavigateSettings?.("ai") }}
    >
      <FeatureCard>
        <FeatureRow title="Enable Triage Intelligence" description="Get suggestions for related issues, teams, assignees, projects, and labels">
          <Toggle checked={enabled} disabled={busy} label="Enable Triage Intelligence" onChange={(value) => setEnabled("triage-intelligence", value)} />
        </FeatureRow>
      </FeatureCard>
      <FeatureSection title="Behavior" description="Define rules for showing, hiding, or auto-applying suggestions" disabled={!enabled}>
        <FeatureCard>
          {TRIAGE_ROWS.map(([key, title, Icon]) => (
            <FeatureRow key={key} icon={Icon} title={title}>
              <FeatureSelect label={title} value={config[key] as string} options={options} disabled={busy || !enabled} onChange={(value) => update(key, value as never)} />
            </FeatureRow>
          ))}
        </FeatureCard>
      </FeatureSection>
      <FeatureSection title="Workspace guidance" description="Optionally provide additional context and instructions for Triage Intelligence in this workspace" disabled={!enabled}>
        <GuidanceField label="Triage Intelligence guidance" value={config.workspaceGuidance ?? ""} disabled={busy || !enabled} onSave={(value) => update("workspaceGuidance", value)} />
      </FeatureSection>
    </FeatureShell>
  );
}

/** Settings › AI & agents › Resolved thread summaries. */
export function ResolvedSummariesSettings({ settings, busy, setEnabled, onNavigateSettings }: AiPageProps) {
  return (
    <FeatureShell className="feature-ai-settings" title="Resolved thread summaries" description="Automatically generate summaries for resolved threads" back={{ ...AI_BACK, onClick: () => onNavigateSettings?.("ai") }}>
      <FeatureCard>
        <FeatureRow title="Enable thread summaries" description="Generate summaries for resolved threads that don't have a resolving reply comment">
          <Toggle checked={settings.featureFlags["thread-summaries"] ?? true} disabled={busy} label="Enable thread summaries" onChange={(value) => setEnabled("thread-summaries", value)} />
        </FeatureRow>
      </FeatureCard>
    </FeatureShell>
  );
}

/** Settings › AI & agents › Flow Agent › Updates: how initiative and project updates should be written. */
export function UpdateGuidanceSettings({ settings, busy, setFeature, onNavigateSettings }: AiPageProps) {
  const isEditable = !busy;
  return (
    <FeatureShell className="feature-ai-settings" title="Initiative and project updates" description="Customize how project and initiative updates should be written" back={{ ...AI_BACK, onClick: () => onNavigateSettings?.("ai-agent") }}>
      <FeatureSection title="Initiative updates">
        <GuidanceField label="Initiative updates" placeholder="Optional guidance..." value={settings.featureSettings.initiativeUpdateGuidance ?? ""} disabled={!isEditable} onSave={(value) => setFeature("initiativeUpdateGuidance", value)} />
      </FeatureSection>
      <FeatureSection title="Project updates">
        <GuidanceField label="Project updates" placeholder="Optional guidance..." value={settings.featureSettings.projectUpdateGuidance ?? ""} disabled={!isEditable} onSave={(value) => setFeature("projectUpdateGuidance", value)} />
      </FeatureSection>
    </FeatureShell>
  );
}

/** Settings › Agents (reached from AI & agents): the installed agents and the guidance they receive. */
export function InstalledAgentsSettings({ data, settings, busy, onReload, onNavigateSettings }: AiPageProps) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const workspace = data.workspace.urlKey;
  const isAdmin = ["admin", "owner"].includes(data.viewerRole);
  const canEditGuidance = isAdmin || settings.agentGuidancePermission === "members";
  const [applications, setApplications] = useState<ApplicationInstallation[]>([]);
  useEffect(() => {
    if (!isAdmin) return;
    let active = true;
    void listApplications(workspace).then((items) => active && setApplications(items.filter((item) => item.active))).catch(() => undefined);
    return () => {
      active = false;
    };
  }, [workspace, isAdmin]);
  const saveGuidance = async (value: string) => {
    try {
      await updateWorkspaceAgentGuidance(value);
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
  };
  const count = applications.length;
  const open = (item: ApplicationInstallation) => navigate(`${settingsPath(workspace, "applications")}/${encodeURIComponent(item.clientId)}`);
  const rows: ReactNode = applications.map((item) => (
    <button key={item.id} type="button" className="feature-row feature-row-link" onClick={() => open(item)}>
      <span className="feature-row-icon feature-agent-avatar" aria-hidden="true" data-i18n-ignore>{initials(item.name)}</span>
      <div>
        <strong data-i18n-ignore>{item.name}</strong>
        <span>{item.allPublicTeams ? t("Access to all public teams") : t("Access to {count} teams").replace("{count}", String(item.teamIds.length))}</span>
      </div>
      <aside><ChevronRight size={15} aria-hidden="true" /></aside>
    </button>
  ));
  return (
    <FeatureShell
      className="feature-ai-settings"
      title="Agents"
      description={<>{t("AI agents can work alongside you as teammates. Tackle complex tasks together or delegate entire issues end-to-end.")} <DocsLink href={DOCS_AGENTS} /></>}
      back={{ ...AI_BACK, onClick: () => onNavigateSettings?.("ai") }}
    >
      <FeatureCard>
        <div className="feature-row feature-agents-summary">
          <div>
            <strong>{count === 1 ? t("1 agent enabled") : t("{count} agents enabled").replace("{count}", String(count))}</strong>
            <span>
              {t("Browse")}{" "}
              <button type="button" className="feature-section-link" onClick={() => onNavigateSettings?.("integrations")}>{t("integrations")}</button>{" "}
              {t("to enable new agents")}
            </span>
          </div>
          {isAdmin && <aside><ApplicationMembers data={data} onReload={onReload} /></aside>}
        </div>
        {rows}
      </FeatureCard>
      <FeatureSection title="Workspace guidance" description="An agent automatically receives the full issue context when working on an issue. You can provide additional workspace-wide guidance, though adherence is controlled by the agent integration.">
        <GuidanceField label="Installed agents guidance" value={settings.agentInstructions ?? ""} disabled={busy || !canEditGuidance} onSave={saveGuidance} />
      </FeatureSection>
    </FeatureShell>
  );
}

/** Settings › AI & agents › Code Intelligence, wired to the workspace's GitHub code access. */
export function CodeIntelligenceSettings({ data, settings, busy, setEnabled, setFeature, onReload, onNavigateSettings, onOpenIntegration }: AiPageProps) {
  const repositoryAccess: RepositoryAccessSettings = { ...DEFAULT_REPOSITORY_ACCESS, ...(settings.featureSettings.repositoryAccess ?? {}) };
  const { t } = useI18n();
  const grantCodeAccess = async () => {
    const connection = githubConnection(data);
    if (!connection) {
      onOpenIntegration("github");
      return;
    }
    try {
      await updateIntegrationConnection(connection.provider, connection.id, { config: { ...(connection.config ?? {}), codeAccess: "true" } });
      await onReload();
      toast.success(t("Code access enabled for GitHub"));
    } catch (error) {
      toast.error(message(error));
    }
  };
  return (
    <CodeIntelligencePage
      data={data}
      settings={settings}
      busy={busy}
      setEnabled={setEnabled}
      onBack={() => onNavigateSettings?.("ai")}
      onOpenGitHub={() => onOpenIntegration("github")}
      onGrantCodeAccess={() => void grantCodeAccess()}
      repositoryAccess={repositoryAccess}
      onRepositoryAccessChange={(next) => setFeature("repositoryAccess", next)}
    />
  );
}
