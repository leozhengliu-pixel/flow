import {
  useEffect,
  useRef,
  useState,
  type ComponentType,
  type KeyboardEvent,
} from "react";
import * as Popover from "@radix-ui/react-popover";
import {
  ArrowLeft,
  Bot,
  Check,
  ChevronRight,
  Circle,
  Copy,
  GitBranch,
  Mail,
  MoreHorizontal,
  Plus,
  RefreshCw,
  Sparkles,
  Trash2,
  WandSparkles,
  X,
} from "lucide-react";
import { toast } from "sonner";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  createDocumentTemplate,
  createEmailIntakeAddress,
  createIssue,
  createTeamLabel,
  createTriageResponsibility,
  createTriageRule,
  createWorkflowState,
  deleteDocumentTemplate,
  deleteEmailIntakeAddress,
  deleteGitAutomation,
  deleteTargetBranch,
  deleteTeam,
  deleteTeamLabel,
  deleteTriageResponsibility,
  deleteTriageRule,
  deleteWorkflowState,
  fetchWorkflowStates,
  reorderWorkflowStates,
  setTeamMembership,
  updateCycleSettings,
  rotateEmailIntakeAddress,
  updateDocumentTemplate,
  updateIssue,
  updateStructuredTeamSettings,
  updateTeam,
  updateTeamLabel,
  updateWorkflowState,
  upsertGitAutomation,
  upsertTargetBranch,
} from "@/lib/api";
import type {
  BootstrapData,
  CycleSettings,
  DocumentTemplate,
  IssueLabel,
  IssueTemplate,
  ProjectTemplate,
  Team,
  TeamSettings,
  WorkflowState,
  WorkflowStateType,
} from "@/types/flow";
import { loopsPath, type TeamSettingsSection } from "@/lib/app-routes";
import { TemplateEditor } from "./advanced-settings";
import {
  SettingsRow,
  SettingsSection,
  SettingsSelect as BaseSettingsSelect,
  SettingsToggle,
  type SettingsSelectOption,
} from "./settings-primitives";
import { CheckboxMark } from "@/components/ui/checkbox-mark";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { StatusIcon } from "@/components/issue/issue-icons";
import {
  ViewGlyph,
  ViewIconPicker,
  type ViewVisual,
} from "@/components/views/view-icon-picker";
import { useI18n } from "@/i18n/i18n";
import {
  StatusColorPicker,
  StatusDragHandle,
  StatusMenuIcon,
} from "./issues-projects-settings";

import "./issues-projects-settings.css";

const SECTIONS: {
  id: TeamSettingsSection;
  label: string;
  description: string;
}[] = [
  {
    id: "general",
    label: "General",
    description: "Name, identifier, timezone, estimates, and broader settings",
  },
  {
    id: "security",
    label: "Access and permissions",
    description:
      "Manage team access and who in the team can take certain actions",
  },
  { id: "members", label: "Members", description: "Manage team members" },
  {
    id: "notifications",
    label: "Slack notifications",
    description: "Broadcast notifications to Slack",
  },
  {
    id: "issue-labels",
    label: "Issue labels",
    description: "Labels available to this team’s issues",
  },
  {
    id: "templates",
    label: "Templates",
    description: "Pre-filled templates for issues, documents, and projects",
  },
  {
    id: "recurring-issues",
    label: "Recurring issues",
    description: "Automatically create issues on a schedule",
  },
  {
    id: "statuses",
    label: "Issue statuses",
    description: "Customize the statuses issues go through",
  },
  {
    id: "workflow",
    label: "Workflows & automations",
    description: "Manage issue automations, git workflows and other workflows",
  },
  {
    id: "triage",
    label: "Triage",
    description: "Streamline how you handle requests from outside your team",
  },
  {
    id: "cycles",
    label: "Cycles",
    description: "Focus your team over short, time-boxed windows",
  },
  {
    id: "agents",
    label: "Team agents",
    description: "Add guidance for how agents should operate within this team",
  },
  {
    id: "agent-skills",
    label: "Agent skills",
    description: "Agent skills shared with this team",
  },
  {
    id: "ai-updates",
    label: "Project updates",
    description:
      "Automatically generate updates using recent activity and defined rules",
  },
  {
    id: "ai-summaries",
    label: "Resolved thread summaries",
    description: "Automatically generate summaries for resolved threads",
  },
];
const STATUS_GROUPS: {
  type: WorkflowStateType;
  label: string;
  reserved?: boolean;
}[] = [
  { type: "backlog", label: "Backlog" },
  { type: "unstarted", label: "Unstarted" },
  { type: "started", label: "Started" },
  { type: "completed", label: "Completed" },
  { type: "canceled", label: "Canceled" },
  { type: "canceled", label: "Duplicate", reserved: true },
];

export function TeamWorkflowSettings({
  data,
  team,
  section,
  onNavigate,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  section: TeamSettingsSection;
  onNavigate: (section: TeamSettingsSection) => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  if (section === "statuses")
    return (
      <StatusesSettings
        data={data}
        team={team}
        onBack={() => onNavigate("overview")}
        onReload={onReload}
      />
    );
  const Content = TEAM_SECTION_COMPONENTS[section];
  return (
    <>
      <header className="settings-page-header team-settings-header">
        <div>
          {section !== "overview" && (
            <button
              className="settings-icon-action team-settings-back"
              aria-label={t("Back to team settings")}
              onClick={() => onNavigate("overview")}
            >
              <ArrowLeft size={15} />
            </button>
          )}
          <span
            className="settings-team-icon large"
            style={{ color: team.color }}
          >
            <ViewGlyph color={team.color} icon={team.icon || "Team"} />
          </span>
          <h1>
            {section === "overview" ? (
              <span data-i18n-ignore>{team.name}</span>
            ) : (
              t(SECTIONS.find((item) => item.id === section)?.label ?? "")
            )}
          </h1>
        </div>
      </header>
      {section === "overview" && (
        <TeamOverview
          data={data}
          team={team}
          onNavigate={onNavigate}
          onReload={onReload}
        />
      )}
      {section === "agents" && <TeamAgentsSettings data={data} />}
      {Content && <Content data={data} team={team} onReload={onReload} />}
    </>
  );
}

type TeamSectionComponent = ComponentType<{
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}>;
const TEAM_SECTION_COMPONENTS: Partial<
  Record<TeamSettingsSection, TeamSectionComponent>
> = {
  general: GeneralSettings,
  security: AccessSettings,
  members: MembersSettings,
  notifications: SlackSettings,
  "issue-labels": LabelsSettings,
  templates: TemplatesSettings,
  "recurring-issues": RecurringIssuesSettings,
  workflow: WorkflowSettings,
  triage: TriageSettings,
  cycles: CyclesSettings,
  "agent-skills": AgentSkillsSettings,
  "ai-updates": ProjectUpdatePromptSettings,
  "ai-summaries": ResolvedSummariesSettings,
};

function useTeamSettings(
  data: BootstrapData,
  team: Team,
  onReload: () => Promise<void>,
) {
  const [settings, setSettings] = useState(
    data.teamSettings?.[team.id] ?? defaultTeamSettings(team.id, data.states),
  );
  useEffect(
    () =>
      setSettings(
        data.teamSettings?.[team.id] ??
          defaultTeamSettings(team.id, data.states),
      ),
    [data.states, data.teamSettings, team.id],
  );
  const save = async (patch: Partial<TeamSettings>) => {
    const persisted =
      data.teamSettings?.[team.id] ?? defaultTeamSettings(team.id, data.states);
    const changed = Object.fromEntries(
      Object.entries(patch).filter(
        ([key, value]) =>
          JSON.stringify(value) !==
          JSON.stringify(persisted[key as keyof TeamSettings]),
      ),
    ) as Partial<TeamSettings>;
    if (!Object.keys(changed).length) return;
    const previous = settings;
    setSettings({ ...settings, ...changed });
    try {
      const next = await updateStructuredTeamSettings(team.id, changed);
      setSettings(next);
      await onReload();
    } catch (error) {
      setSettings(previous);
      toast.error(message(error));
    }
  };
  return { settings, setSettings, save };
}

function TeamOverview({
  data,
  team,
  onNavigate,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onNavigate: (section: TeamSettingsSection) => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const { settings, save } = useTeamSettings(data, team, onReload);
  const retire = async () => {
    const action = team.retiredAt ? "Restore" : "Retire";
    if (
      !(await confirmAction(`${t(action)} ${team.name}?`, {
        confirmLabel: t(team.retiredAt ? "Restore team" : "Retire team"),
        danger: !team.retiredAt,
      }))
    )
      return;
    await updateTeam(data.workspace.urlKey, team.id, {
      retired: !team.retiredAt,
    });
    await onReload();
  };
  const remove = async () => {
    if (
      !(await confirmAction(`Delete ${team.name}?`, {
        description: t("This permanently deletes the team and its owned data."),
        confirmLabel: t("Delete team"),
      }))
    )
      return;
    await deleteTeam(data.workspace.urlKey, team.id);
    await onReload();
  };
  const leave = async () => {
    if (
      !(await confirmAction(`Leave ${team.name}?`, {
        confirmLabel: t("Leave team"),
      }))
    )
      return;
    await setTeamMembership(
      data.workspace.urlKey,
      team.id,
      data.viewer.id,
      false,
      "member",
    );
    await onReload();
  };
  const group = (title: string | undefined, ids: TeamSettingsSection[]) => (
    <TeamSection title={title}>
      <div className="team-overview-list">
        {ids.map((id) => {
          const item = SECTIONS.find((value) => value.id === id)!;
          return (
            <button key={item.id} onClick={() => onNavigate(item.id)}>
              <span>
                <strong>{t(item.label)}</strong>
                <small>{t(item.description)}</small>
              </span>
              <ChevronRight size={15} />
            </button>
          );
        })}
      </div>
    </TeamSection>
  );
  return (
    <>
      {group(undefined, ["general", "security", "members", "notifications"])}
      {group(t("Issues, projects, and docs"), [
        "issue-labels",
        "templates",
        "recurring-issues",
      ])}
      {group(t("Workflow"), ["statuses", "workflow", "triage", "cycles"])}
      <TeamSection title={t("AI & Agents")}>
        <div className="team-overview-list">
          {["agents", "agent-skills"].map((id) => {
            const item = SECTIONS.find((value) => value.id === id)!;
            return <button key={item.id} onClick={() => onNavigate(item.id)}><span><strong>{t(item.label)}</strong><small>{t(item.description)}</small></span><ChevronRight size={15}/></button>;
          })}
          <a href={loopsPath(data.workspace.urlKey)}>
            <span><strong>Loops</strong><small>{t("Automated agent workflows that run on a schedule or when an issue is updated")}</small></span>
            <ChevronRight size={15}/>
          </a>
          {["ai-updates", "ai-summaries"].map((id) => {
            const item = SECTIONS.find((value) => value.id === id)!;
            return <button key={item.id} onClick={() => onNavigate(item.id)}><span><strong>{t(item.label)}</strong><small>{t(item.description)}</small></span><ChevronRight size={15}/></button>;
          })}
        </div>
      </TeamSection>
      <TeamSection
        title="Team hierarchy"
        description={<span>Teams can be nested to reflect your team structure and to share workflows and settings. <a href="https://linear.app/docs/sub-teams" rel="noreferrer" target="_blank">Docs↗</a></span>}
      >
        <SelectRow
          title="Parent team"
          value={settings.parentTeamId ?? ""}
          options={[
            "",
            ...data.teams
              .filter((item) => item.id !== team.id && !item.retiredAt)
              .map((item) => item.id),
          ]}
          labels={{
            "": "No parent team",
            ...Object.fromEntries(
              data.teams.map((item) => [item.id, item.name]),
            ),
          }}
          entityOptions={data.teams.map((item) => item.id)}
          onChange={(value) => save({ parentTeamId: value })}
        />
      </TeamSection>
      <TeamSection
        title="Team initiatives"
        description="Control whether initiatives are shown in this team's sidebar."
      >
        <ToggleRow
          title="Show initiatives in the sidebar"
          description="Display this team's initiatives in the sidebar"
          checked={settings.showInitiatives}
          onChange={(value) => save({ showInitiatives: value })}
        />
      </TeamSection>
      <TeamSection title="Danger zone">
        <TeamRow
          title="Leave team"
          description="Remove yourself as a member of this team"
        >
          <button
            className="settings-action danger"
            disabled={
              !data.teamMembers.some(
                (item) =>
                  item.teamId === team.id && item.userId === data.viewer.id,
              )
            }
            onClick={() => void leave()}
          >
            Leave team…
          </button>
        </TeamRow>
        <TeamRow
          title={team.retiredAt ? "Restore team" : "Retire team"}
          description="Prevent creating and updating issues while preserving historical data"
        >
          <button
            className="settings-action danger"
            onClick={() => void retire()}
          >
            {team.retiredAt ? "Restore…" : "Retire…"}
          </button>
        </TeamRow>
        <TeamRow
          title="Delete team"
          description="Permanently delete this team and all of its owned data"
        >
          <button
            className="settings-action danger"
            disabled={data.teams.filter((item) => !item.retiredAt).length <= 1}
            onClick={() => void remove()}
          >
            Delete…
          </button>
        </TeamRow>
      </TeamSection>
    </>
  );
}

function GeneralSettings({
  data,
  team,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const initial =
    data.teamSettings?.[team.id] ?? defaultTeamSettings(team.id, data.states);
  const [settings, setSettings] = useState(initial);
  const [name, setName] = useState(team.name);
  const [identifier, setIdentifier] = useState(team.key);
  const [icon, setIcon] = useState(team.icon || "Team");
  const [color, setColor] = useState(team.color);
  useEffect(() => {
    setSettings(
      data.teamSettings?.[team.id] ?? defaultTeamSettings(team.id, data.states),
    );
    setName(team.name);
    setIdentifier(team.key);
    setIcon(team.icon || "Team");
    setColor(team.color);
  }, [
    data.states,
    data.teamSettings,
    team.color,
    team.icon,
    team.id,
    team.key,
    team.name,
  ]);
  const save = async (
    patch: Partial<TeamSettings> & { identifier?: string },
  ) => {
    const persisted = data.teamSettings?.[team.id] ?? initial;
    const changed = Object.fromEntries(
      Object.entries(patch).filter(([key, value]) =>
        key === "identifier"
          ? !Object.is(value, team.key)
          : JSON.stringify(value) !==
            JSON.stringify(persisted[key as keyof TeamSettings]),
      ),
    ) as Partial<TeamSettings> & { identifier?: string };
    if (!Object.keys(changed).length) return;
    try {
      setSettings(await updateStructuredTeamSettings(team.id, changed));
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
  };
  const saveVisual = async ({
    icon: nextIcon,
    color: nextColor,
  }: ViewVisual) => {
    if (
      nextIcon === (team.icon || "Team") &&
      nextColor.toLowerCase() === team.color.toLowerCase()
    )
      return;
    setIcon(nextIcon);
    setColor(nextColor);
    try {
      await updateTeam(data.workspace.urlKey, team.id, {
        icon: nextIcon,
        color: nextColor,
      });
      await onReload();
    } catch (error) {
      setIcon(team.icon || "Team");
      setColor(team.color);
      toast.error(message(error));
    }
  };
  return (
    <>
      <TeamSection title="Icon & Name">
        <TeamRow title="Icon & Name">
          <div className="team-icon-name-control">
            <ViewIconPicker
              color={color}
              icon={icon}
              onChange={(visual) => void saveVisual(visual)}
              prependTeam
              triggerClassName="team-icon-trigger"
            />
            <input
              className="settings-input"
              aria-label="Icon & Name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              onBlur={() => {
                if (name.trim() && name.trim() !== team.name)
                  void updateTeam(data.workspace.urlKey, team.id, {
                    name: name.trim(),
                  }).then(onReload);
              }}
            />
          </div>
        </TeamRow>
        <InputRow
          title="Identifier"
          description="Used as the prefix for new issue identifiers. Existing issue identifiers won't change."
          value={identifier}
          onChange={(value) =>
            setIdentifier(
              value
                .toUpperCase()
                .replace(/[^A-Z0-9]/g, "")
                .slice(0, 5),
            )
          }
          onCommit={(value) => save({ identifier: value })}
        />
      </TeamSection>
      <TeamSection
        title="Description"
        description="A short summary shown on the team page"
      >
        <InputRow
          title="Description"
          value={settings.description ?? ""}
          onChange={(value) =>
            setSettings((current) => ({ ...current, description: value }))
          }
          onCommit={(value) => save({ description: value })}
        />
      </TeamSection>
      <TeamSection
        title="Timezone"
        description="Used for team schedules, dates, and cycle start times"
      >
        <SelectRow
          title="Timezone"
          value={settings.timezone}
          options={[
            "Etc/UTC",
            "Asia/Shanghai",
            "America/Los_Angeles",
            "Europe/London",
          ]}
          onChange={(value) => save({ timezone: value })}
        />
      </TeamSection>
      <TeamSection title="Estimates">
        <SelectRow
          title="Issue estimation"
          description="Used to estimate issue complexity and plan cycle capacity."
          value={settings.estimateType}
          options={["notUsed", "exponential", "fibonacci", "flow"]}
          labels={{
            notUsed: "Not in use",
            exponential: "Exponential",
            fibonacci: "Fibonacci",
            flow: "Flow",
          }}
          onChange={(value) =>
            save({ estimateType: value as TeamSettings["estimateType"] })
          }
        />
      </TeamSection>
      <EmailIntakeSettings
        data={data}
        team={team}
        settings={settings}
        save={save}
        onReload={onReload}
      />
      <TeamSection title="Other">
        <ToggleRow
          title="Enable detailed issue history"
          description="Each change to an issue receives and persists a distinct history entry, creating a more detailed history for auditing purposes."
          checked={settings.detailedHistory}
          onChange={(value) => save({ detailedHistory: value })}
        />
      </TeamSection>
    </>
  );
}

function EmailIntakeSettings({
  data,
  team,
  settings,
  save,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  settings: TeamSettings;
  save: (patch: Partial<TeamSettings>) => Promise<void>;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const addresses = data.emailIntakeAddresses.filter(
    (item) => item.teamId === team.id && item.enabled,
  );
  const [configuring, setConfiguring] = useState(false),
    [localPart, setLocalPart] = useState(team.key.toLowerCase()),
    [domain, setDomain] = useState("");
  const create = async () => {
    if (!localPart.trim() || !domain.trim()) return;
    try {
      await createEmailIntakeAddress(team.id, {
        localPart: localPart.trim(),
        domain: domain.trim(),
      });
      await save({ issueEmailEnabled: true });
      setConfiguring(false);
      await onReload();
      toast.success(t("Issue intake email created"));
    } catch (error) {
      toast.error(message(error));
    }
  };
  const disable = async () => {
    try {
      await Promise.all(
        addresses.map((item) => deleteEmailIntakeAddress(team.id, item.id)),
      );
      await save({ issueEmailEnabled: false });
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
  };
  return (
    <TeamSection title="Create issues by email">
      <TeamRow
        title="Enable issue creation by email"
        description="Use a team-specific email address to create and collaborate on issues via email"
      >
        <SettingsToggle
          checked={settings.issueEmailEnabled}
          label={t("Enable issue creation by email")}
          onChange={(value) => {
            if (value) {
              setConfiguring(true);
              return;
            }
            void disable();
          }}
        />
      </TeamRow>
      {addresses.map((item) => (
        <div className="email-intake-row" key={item.id}>
          <Mail size={16} />
          <span>
            <strong data-i18n-ignore>{item.address}</strong>
            <small>
              {item.verificationState === "verified"
                ? t("Ready to receive email")
                : t("Domain verification pending")}
            </small>
          </span>
          <button
            className="settings-icon-action"
            aria-label={t("Reset email address")}
            onClick={() =>
              void rotateEmailIntakeAddress(team.id, item.id)
                .then(onReload)
                .then(() => toast.success(t("Email address reset")))
                .catch((error) => toast.error(message(error)))
            }
          >
            <RefreshCw size={14} />
          </button>
          <button
            className="settings-action"
            onClick={() =>
              void navigator.clipboard
                .writeText(item.address)
                .then(() =>
                  toast.success(
                    t("Email address successfully copied to clipboard"),
                  ),
                )
            }
          >
            <Copy size={14} />
            {t("Copy")}
          </button>
        </div>
      ))}
      {configuring && (
        <form
          className="workflow-rule-create"
          onSubmit={(event) => {
            event.preventDefault();
            void create();
          }}
        >
          <input
            autoFocus
            className="settings-input"
            aria-label={t("Email local part")}
            placeholder="issues"
            value={localPart}
            onChange={(event) =>
              setLocalPart(
                event.target.value.toLowerCase().replace(/[^a-z0-9._-]/g, ""),
              )
            }
          />
          <span className="email-at">@</span>
          <input
            className="settings-input"
            aria-label={t("Email domain")}
            placeholder="mail.example.com"
            value={domain}
            onChange={(event) => setDomain(event.target.value)}
          />
          <button
            type="button"
            className="settings-action"
            onClick={() => setConfiguring(false)}
          >
            {t("Cancel")}
          </button>
          <button
            className="settings-action primary"
            disabled={!localPart.trim() || !domain.trim()}
          >
            {t("Create")}
          </button>
        </form>
      )}
    </TeamSection>
  );
}

function AccessSettings({
  data,
  team,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const { settings, save } = useTeamSettings(data, team, onReload);
  const permissionLabels = {
    allMembers: "All workspace members",
    teamMembers: "Team members",
    owners: "Team owners",
  };
  const permissionOptions = Object.keys(permissionLabels);
  return (
    <>
      <TeamSection title="Team access">
        <SelectRow
          title="Team visibility"
          description="Private teams are visible only to members."
          value={settings.access}
          options={["public", "private"]}
          labels={{ public: "Public", private: "Private" }}
          onChange={(value) =>
            save({ access: value as TeamSettings["access"] })
          }
        />
        <SelectRow
          title="Who can join"
          value={settings.membershipRestriction}
          options={["open", "members", "owners"]}
          labels={{
            open: "Anyone in the workspace",
            members: "By invitation",
            owners: "Team owners only",
          }}
          onChange={(value) =>
            save({
              membershipRestriction:
                value as TeamSettings["membershipRestriction"],
            })
          }
        />
      </TeamSection>
      <TeamSection title="Management permissions">
        {(
          [
            ["settingsPermission", "Change team settings"],
            ["labelPermission", "Manage labels"],
            ["templatePermission", "Manage templates"],
            ["agentSkillPermission", "Manage agent skills"],
            ["loopPermission", "Create Loops"],
            ["memberPermission", "Manage members"],
          ] as const
        ).map(([key, label]) => (
          <SelectRow
            key={key}
            title={label}
            value={settings[key]}
            options={permissionOptions}
            labels={permissionLabels}
            onChange={(value) =>
              save({ [key]: value } as Partial<TeamSettings>)
            }
          />
        ))}
      </TeamSection>
    </>
  );
}

function SlackSettings({
  data,
  team,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const { settings, setSettings, save } = useTeamSettings(data, team, onReload);
  const slack = data.integrationConnections.find(
    (item) =>
      item.provider.toLowerCase() === "slack" && item.status === "connected",
  );
  const choices = [
    ["issueCreated", "Issue created"],
    ["issueCompleted", "Issue completed"],
    ["issueCanceled", "Issue canceled"],
    ["commentCreated", "New comments"],
    ["projectUpdates", "Project updates"],
    ["cycleUpdates", "Cycle updates"],
  ] as const;
  return (
    <>
      <TeamSection title="Slack connection">
        <TeamRow
          title="Workspace connection"
          description={
            slack
              ? t(`Connected as ${slack.name}`)
              : "Connect Slack from workspace Integrations before choosing a channel."
          }
        >
          <span className="settings-static">
            {slack ? "Connected" : "Not connected"}
          </span>
        </TeamRow>
        <InputRow
          title="Channel"
          description="Slack channel name, for example #engineering"
          value={settings.slackChannelName ?? ""}
          onChange={(value) =>
            setSettings((current) => ({ ...current, slackChannelName: value }))
          }
          onCommit={(value) => save({ slackChannelName: value })}
        />
      </TeamSection>
      <div
        className={
          !slack || !settings.slackChannelName ? "settings-disabled-area" : ""
        }
      >
        <TeamSection title="Notifications">
          {choices.map(([key, label]) => (
            <ToggleRow
              key={key}
              title={label}
              checked={settings.slackNotifications[key] ?? false}
              onChange={(value) =>
                save({
                  slackNotifications: {
                    ...settings.slackNotifications,
                    [key]: value,
                  },
                })
              }
            />
          ))}
        </TeamSection>
      </div>
    </>
  );
}

function RecurringIssuesSettings({
  data,
  team,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const { formatDate, t } = useI18n();
  const issues = data.issues.filter(
    (issue) => issue.team.id === team.id && issue.recurrence,
  );
  const [creating, setCreating] = useState(false);
  const [title, setTitle] = useState("");
  const [cadence, setCadence] = useState<"daily" | "weekly" | "monthly">(
    "weekly",
  );
  const create = async () => {
    if (!title.trim()) return;
    try {
      const issue = await createIssue({
        title: title.trim(),
        description: "",
        teamId: team.id,
      });
      await updateIssue(issue.id, { recurrence: cadence });
      setCreating(false);
      setTitle("");
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
  };
  return (
    <TeamSection
      title="Recurring issues"
      action={
        <button className="settings-action" onClick={() => setCreating(true)}>
          <Plus size={13} />
          New recurring issue
        </button>
      }
    >
      <div className="team-setting-list">
        {creating && (
          <form
            className="recurring-issue-create"
            onSubmit={(event) => {
              event.preventDefault();
              void create();
            }}
          >
            <input
              autoFocus
              className="settings-input"
              placeholder="Issue title"
              value={title}
              onChange={(event) => setTitle(event.target.value)}
            />
            <SettingsSelect
              label="Cadence"
              value={cadence}
              onChange={(value) => setCadence(value as typeof cadence)}
              options={["daily", "weekly", "monthly"].map((value) => ({
                value,
                label: titleCase(value),
              }))}
            />
            <button
              type="button"
              className="settings-icon-action"
              aria-label="Cancel"
              onClick={() => setCreating(false)}
            >
              <X size={14} />
            </button>
            <button
              className="settings-action primary"
              disabled={!title.trim()}
            >
              Create
            </button>
          </form>
        )}
        {issues.map((issue) => (
          <div className="recurring-issue-row" key={issue.id}>
          <span>
            <strong data-i18n-ignore>
              {issue.identifier} {issue.title}
            </strong>
              <small>
                {titleCase(issue.recurrence ?? "")}
                {issue.nextOccurrenceAt
                  ? ` · ${t("Next")} ${formatDate(issue.nextOccurrenceAt)}`
                  : ""}
              </small>
            </span>
            <SettingsSelect
              label="Change recurrence cadence"
              value={issue.recurrence ?? "daily"}
              onChange={(value) =>
                void updateIssue(issue.id, {
                  recurrence: value as "daily" | "weekly" | "monthly",
                }).then(onReload)
              }
              options={["daily", "weekly", "monthly"].map((value) => ({
                value,
                label: titleCase(value),
              }))}
            />
          </div>
        ))}
        {!issues.length && !creating && (
          <TeamEmpty
            icon={<Circle size={22} />}
            title="No recurring issues"
            description="Create issues that repeat on a daily, weekly, or monthly schedule."
          />
        )}
      </div>
    </TeamSection>
  );
}

function WorkflowSettings({
  data,
  team,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const { settings, save } = useTeamSettings(data, team, onReload);
  const states = statesForTeam(data, team.id);
  const stateOptions = ["", ...states.map((item) => item.id)];
  const stateLabels: Record<string, string> = {
    "": "No action",
    ...Object.fromEntries(states.map((item) => [item.id, item.name])),
  };
  const prRows = [
    ["draft", "On draft PR open, move to…"],
    ["opened", "On PR open, move to…"],
    ["reviewActivity", "On PR review request or activity, move to…"],
    ["ready", "On PR ready for merge, move to…"],
    ["merged", "On PR merge, move to…"],
  ] as const;
  const branches = data.targetBranches.filter(
      (item) => item.teamId === team.id,
    ),
    [addingBranch, setAddingBranch] = useState(false),
    [branch, setBranch] = useState(""),
    [regex, setRegex] = useState(false),
    [addingRelease, setAddingRelease] = useState(false),
    [releasePipeline, setReleasePipeline] = useState(""),
    [releaseState, setReleaseState] = useState(
      states.find((item) => item.type === "completed")?.id ?? "",
    );
  const savePR = async (key: string, value: string) => {
    await save({ prAutomations: { ...settings.prAutomations, [key]: value } });
    const existing = data.gitAutomationStates.find(
      (item) =>
        item.teamId === team.id &&
        item.repository === "*" &&
        item.event === key,
    );
    if (value)
      await upsertGitAutomation({
        id: existing?.id,
        teamId: team.id,
        repository: "*",
        event: key,
        workflowStateId: value,
        syncComments: true,
        syncLabels: true,
        syncAssignees: true,
        enabled: true,
      });
    else if (existing) await deleteGitAutomation(existing.id);
    await onReload();
  };
  const addBranch = async () => {
    if (!branch.trim()) return;
    try {
      await upsertTargetBranch({
        teamId: team.id,
        repository: regex ? "regex" : "branch",
        branch: branch.trim(),
        default: branches.length === 0,
      });
      setBranch("");
      setRegex(false);
      setAddingBranch(false);
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
  };
  const addRelease = async () => {
    if (!releasePipeline || !releaseState) return;
    await save({
      releaseAutomations: [
        ...settings.releaseAutomations,
        {
          id: `release_rule_${Date.now()}`,
          name:
            data.releasePipelines.find((item) => item.id === releasePipeline)
              ?.name ?? "Release automation",
          trigger: releasePipeline,
          action: releaseState,
          enabled: true,
        },
      ],
    });
    setAddingRelease(false);
  };
  return (
    <>
      <TeamSection title="Pull request automations">
        {prRows.map(([key, label]) => (
          <SelectRow
            key={key}
            title={label}
            value={settings.prAutomations[key] ?? ""}
            options={stateOptions}
            labels={stateLabels}
            entityOptions={states.map((item) => item.id)}
            onChange={(value) => savePR(key, value)}
          />
        ))}
      </TeamSection>
      <TeamSection
        title="Branch-specific rules"
        action={
          <button
            className="settings-action"
            disabled={addingBranch}
            onClick={() => setAddingBranch(true)}
          >
            <Plus size={13} />
            Add branch
          </button>
        }
      >
        <p className="settings-section-copy">
          Set different rules for target branches, e.g. when a PR is merged into
          a specified branch.
        </p>
        {branches.map((item) => (
          <div className="automation-rule-row" key={item.id}>
            <GitBranch size={16} />
            <span>
              <strong data-i18n-ignore>{item.branch}</strong>
              <small>
                {item.repository === "regex"
                  ? "Regular expression"
                  : "Branch pattern"}
                {item.default ? " · Default" : ""}
              </small>
            </span>
            <Menu
              onDelete={() =>
                void deleteTargetBranch(item.id)
                  .then(onReload)
                  .catch((error) => toast.error(message(error)))
              }
            />
          </div>
        ))}
        {addingBranch && (
          <form
            className="workflow-rule-create"
            onSubmit={(event) => {
              event.preventDefault();
              void addBranch();
            }}
          >
            <input
              autoFocus
              className="settings-input"
              aria-label="Branch pattern"
              placeholder="branch name"
              value={branch}
              onChange={(event) => setBranch(event.target.value)}
            />
            <label className="workflow-inline-check">
              <input
                type="checkbox"
                checked={regex}
                onChange={(event) => setRegex(event.target.checked)}
              />
              Use regex
            </label>
            <button
              type="button"
              className="settings-action"
              onClick={() => setAddingBranch(false)}
            >
              Cancel
            </button>
            <button
              className="settings-action primary"
              disabled={!branch.trim()}
            >
              Submit
            </button>
          </form>
        )}
      </TeamSection>
      <TeamSection
        title="Release automations"
        action={
          !addingRelease ? (
            <button
              className="settings-action"
              onClick={() => setAddingRelease(true)}
            >
              <Plus size={13} />
              Add rule
            </button>
          ) : undefined
        }
      >
        <p className="settings-section-copy">
          Automatically update linked issues when a release is completed.
        </p>
        {settings.releaseAutomations.map((rule) => (
          <div className="automation-rule-row" key={rule.id}>
            <span>
              <strong data-i18n-ignore>{rule.name}</strong>
              <small>
                On release completion →{" "}
                <span data-i18n-ignore>
                  {stateLabels[rule.action] ?? rule.action}
                </span>
              </small>
            </span>
            <Menu
              onDelete={() =>
                void save({
                  releaseAutomations: settings.releaseAutomations.filter(
                    (item) => item.id !== rule.id,
                  ),
                })
              }
            />
          </div>
        ))}
        {!settings.releaseAutomations.length && !addingRelease && (
          <TeamEmpty
            icon={<GitBranch size={22} />}
            title="No release automations yet"
            description="Add a rule to update linked issues when a release completes."
          />
        )}
        {addingRelease && (
          <form
            className="workflow-rule-create"
            onSubmit={(event) => {
              event.preventDefault();
              void addRelease();
            }}
          >
            <SettingsSelect
              label="Select pipeline"
              value={releasePipeline}
              onChange={setReleasePipeline}
              options={[
                { value: "", label: "All production pipelines" },
                ...data.releasePipelines
                  .filter(
                    (item) => item.production && item.teamIds.includes(team.id),
                  )
                  .map((item) => ({
                    value: item.id,
                    label: item.name,
                    entityName: true,
                  })),
              ]}
            />
            <SettingsSelect
              label="On release completion, move issues to…"
              value={releaseState}
              onChange={setReleaseState}
              options={states.map((item) => ({
                value: item.id,
                label: item.name,
                entityName: true,
              }))}
            />
            <button
              type="button"
              className="settings-action"
              onClick={() => setAddingRelease(false)}
            >
              Cancel
            </button>
            <button
              className="settings-action primary"
              disabled={!releasePipeline || !releaseState}
            >
              Save
            </button>
          </form>
        )}
      </TeamSection>
      <TeamSection title="Auto-close automations">
        <ToggleRow
          title="Auto-close parent issues"
          description="Automatically close an open parent issue when its last sub-issue is closed"
          checked={settings.autoCloseParents}
          onChange={(value) => save({ autoCloseParents: value })}
        />
        <ToggleRow
          title="Auto-close sub-issues"
          description="Automatically close all sub-issues when their parent issue is closed"
          checked={settings.autoCloseSubIssues}
          onChange={(value) => save({ autoCloseSubIssues: value })}
        />
        <ToggleRow
          title="Auto-close stale issues"
          description="Automatically close issues that haven't been completed, canceled, or updated in…"
          checked={settings.autoCloseStale}
          onChange={(value) => save({ autoCloseStale: value })}
        />
        {settings.autoCloseStale && (
          <>
            <NumberRow
              title="Close after being stale for"
              description="Months without activity"
              value={settings.staleMonths}
              onCommit={(value) => save({ staleMonths: value })}
            />
            <SelectRow
              title="When closing stale issues, set status to"
              value={settings.staleStatusId ?? ""}
              options={stateOptions}
              labels={stateLabels}
              entityOptions={states.map((item) => item.id)}
              onChange={(value) => save({ staleStatusId: value })}
            />
          </>
        )}
      </TeamSection>
      <TeamSection title="Auto-archive closed issues, cycles, and projects">
        <NumberRow
          title="Auto-archive closed items after"
          description="Months after completion"
          value={settings.autoArchiveMonths}
          onCommit={(value) => save({ autoArchiveMonths: value })}
        />
      </TeamSection>
      <TeamSection title="Re-order issues when moved to a new status">
        <SelectRow
          title="When progressing status, place issues…"
          value={settings.progressOrder}
          options={["first", "last", "noAction"]}
          labels={{ first: "First", last: "Last", noAction: "No action" }}
          onChange={(value) =>
            save({ progressOrder: value as TeamSettings["progressOrder"] })
          }
        />
      </TeamSection>
    </>
  );
}

function TriageSettings({
  data,
  team,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const { formatNumber, t } = useI18n();
  const { settings, save } = useTeamSettings(data, team, onReload);
  const responsibilities = data.triageResponsibilities.filter(
      (item) => item.teamId === team.id,
    ),
    rules = data.triageRoutingRules
      .filter((item) => item.teamId === team.id)
      .sort((a, b) => a.position - b.position);
  const members = data.teamMembers
    .filter((item) => item.teamId === team.id)
    .map((item) => data.users.find((user) => user.id === item.userId))
    .filter(Boolean) as BootstrapData["users"];
  const [name, setName] = useState(""),
    [userId, setUserId] = useState(""),
    [ruleName, setRuleName] = useState(""),
    [responsibilityId, setResponsibilityId] = useState("");
  const addResponsibility = async () => {
    if (!name.trim() || !userId) return;
    try {
      await createTriageResponsibility(team.id, {
        name: name.trim(),
        mode: "individual",
        userIds: [userId],
      });
      setName("");
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
  };
  const addRule = async () => {
    if (!ruleName.trim() || !responsibilityId) return;
    try {
      await createTriageRule(team.id, {
        name: ruleName.trim(),
        conditions: {},
        responsibilityId,
        labelIds: [],
      });
      setRuleName("");
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
  };
  return (
    <>
      <TeamSection>
        <ToggleRow
          title="Enable triage for this team"
          description="Issues added to this team by outside members are sent to the triage inbox first"
          checked={settings.triageEnabled}
          onChange={(value) => save({ triageEnabled: value })}
        />
        <ToggleRow
          title="Require explicit prioritization"
          description="Require that a priority has to be set when moving issues out of triage"
          checked={settings.triageRequirePriority}
          onChange={(value) => save({ triageRequirePriority: value })}
        />
      </TeamSection>
      <TeamSection title="Triage responsibility">
        <p className="settings-section-copy">
          Define how incoming issues and requests are handled in triage
        </p>
        <SelectRow
          title="Action"
          description="When a new issue is added to triage, take the following action"
          value={settings.triageAction}
          options={["none", "creator", "teamOwner", "responsibility"]}
          labels={{
            none: "No action",
            creator: "Assign to issue creator",
            teamOwner: "Assign to team owner",
            responsibility: "Use responsibility",
          }}
          onChange={(value) => save({ triageAction: value })}
        />
        {responsibilities.map((item) => (
          <div className="automation-rule-row" key={item.id}>
            <span>
              <strong data-i18n-ignore>{item.name}</strong>
              <small>
                {item.mode === "roundRobin" ? "Round robin" : "Individual"} ·{" "}
                <span data-i18n-ignore>
                  {item.userIds
                    .map(
                      (id) =>
                        data.users.find((user) => user.id === id)?.displayName,
                    )
                    .filter(Boolean)
                    .join(", ")}
                </span>
              </small>
            </span>
            <Menu
              onDelete={() =>
                void deleteTriageResponsibility(team.id, item.id)
                  .then(onReload)
                  .catch((error) => toast.error(message(error)))
              }
            />
          </div>
        ))}
        <form
          className="workflow-rule-create"
          onSubmit={(event) => {
            event.preventDefault();
            void addResponsibility();
          }}
        >
          <input
            className="settings-input"
            placeholder="Responsibility name"
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
          <SettingsSelect
            label="Choose member"
            value={userId}
            onChange={setUserId}
            options={[
              { value: "", label: "Choose member…" },
              ...members.map((user) => ({
                value: user.id,
                label: user.displayName,
                entityName: true,
              })),
            ]}
          />
          <button
            className="settings-action"
            disabled={!name.trim() || !userId}
          >
            <Plus size={13} />
            Add
          </button>
        </form>
      </TeamSection>
      <TeamSection
        title="Triage rules"
        action={
          <button
            className="settings-action"
            disabled={!responsibilities.length}
            onClick={() =>
              document
                .querySelector<HTMLInputElement>("#new-triage-rule")
                ?.focus()
            }
          >
            <Plus size={13} />
            Add rule
          </button>
        }
      >
        <p className="settings-section-copy">
          Use rules to automatically process and route triage issues
        </p>
        <form
          className="workflow-rule-create"
          onSubmit={(event) => {
            event.preventDefault();
            void addRule();
          }}
        >
          <input
            id="new-triage-rule"
            className="settings-input"
            placeholder="Rule name"
            value={ruleName}
            onChange={(event) => setRuleName(event.target.value)}
          />
          <SettingsSelect
            label="Choose responsibility"
            value={responsibilityId}
            onChange={setResponsibilityId}
            options={[
              { value: "", label: "Choose responsibility…" },
              ...responsibilities.map((item) => ({
                value: item.id,
                label: item.name,
                entityName: true,
              })),
            ]}
          />
          <button
            className="settings-action"
            disabled={!ruleName.trim() || !responsibilityId}
          >
            Save
          </button>
        </form>
        {rules.map((rule) => (
          <div className="automation-rule-row" key={rule.id}>
            <span>
              <strong data-i18n-ignore>{rule.name}</strong>
              <small>
                All incoming issues →{" "}
                <span data-i18n-ignore>
                  {
                    responsibilities.find(
                      (item) => item.id === rule.responsibilityId,
                    )?.name
                  }
                </span>
              </small>
            </span>
            <Menu
              onDelete={() =>
                void deleteTriageRule(team.id, rule.id)
                  .then(onReload)
                  .catch((error) => toast.error(message(error)))
              }
            />
          </div>
        ))}
        {!rules.length && (
          <TeamEmpty
            icon={<GitBranch size={22} />}
            title="No triage rules yet"
            description="Rules are evaluated in order and the first match owns the issue."
          />
        )}
      </TeamSection>
      <TeamSection title="Agent automations">
        <TeamRow
          title="Loops"
          description={`Loops acting on issues in triage for this team`}
        >
          <span className="settings-static">
            {t(
              `${formatNumber(
                data.loops.filter((item) => item.level === "team").length,
              )} loops`,
            )}
          </span>
        </TeamRow>
      </TeamSection>
      <TeamSection title="Triage Intelligence">
        <p className="settings-section-copy">
          Flow uses agentic AI to automatically find related issues and
          duplicates, and infer properties like team, project, labels, and
          assignee.
        </p>
        <TeamRow title="Triage Intelligence is not enabled in this workspace">
          <span className="settings-static">View settings</span>
        </TeamRow>
      </TeamSection>
    </>
  );
}

function TeamAgentsSettings({ data }: { data: BootstrapData }) {
  const agents = data.integrationConnections.filter(
    (item) =>
      /agent|copilot|cursor|github/i.test(`${item.provider} ${item.name}`) &&
      item.status === "connected",
  );
  return (
    <TeamSection title="Connected agents">
      {agents.map((item) => (
        <TeamRow
          key={item.id}
          title={<span data-i18n-ignore>{item.name}</span>}
          description={<span data-i18n-ignore>{item.provider}</span>}
        >
          <span className="settings-static">Connected</span>
        </TeamRow>
      ))}
      {!agents.length && (
        <TeamEmpty
          icon={<Bot size={24} />}
          title="No team agents"
          description="Agent integrations connected at workspace level will appear here."
        />
      )}
    </TeamSection>
  );
}

function AgentSkillsSettings({
  data,
  team,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const { settings, save } = useTeamSettings(data, team, onReload);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [instructions, setInstructions] = useState("");
  const add = async () => {
    if (!name.trim() || !instructions.trim()) return;
    await save({
      agentSkills: [
        ...settings.agentSkills,
        {
          id: `skill_${Date.now()}`,
          name: name.trim(),
          instructions: instructions.trim(),
          enabled: true,
        },
      ],
    });
    setName("");
    setInstructions("");
    setCreating(false);
  };
  return (
    <TeamSection
      title="Agent skills"
      action={
        <button className="settings-action" onClick={() => setCreating(true)}>
          <Plus size={13} />
          New skill
        </button>
      }
    >
      <div className="team-setting-list">
        {creating && (
          <form
            className="agent-skill-editor"
            onSubmit={(event) => {
              event.preventDefault();
              void add();
            }}
          >
            <input
              autoFocus
              className="settings-input"
              placeholder="Skill name"
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
            <textarea
              className="settings-textarea"
              placeholder="Instructions"
              value={instructions}
              onChange={(event) => setInstructions(event.target.value)}
            />
            <footer>
              <button
                type="button"
                className="settings-action"
                onClick={() => setCreating(false)}
              >
                Cancel
              </button>
              <button
                className="settings-action primary"
                disabled={!name.trim() || !instructions.trim()}
              >
                Create
              </button>
            </footer>
          </form>
        )}
        {settings.agentSkills.map((skill) => (
          <div className="agent-skill-row" key={skill.id}>
            <WandSparkles size={17} />
            <span data-i18n-ignore>
              <strong>{skill.name}</strong>
              <small>{skill.instructions}</small>
            </span>
            <SettingsToggle
              checked={skill.enabled}
              label={t("Enable skill")}
              onChange={(enabled) =>
                save({
                  agentSkills: settings.agentSkills.map((item) =>
                    item.id === skill.id ? { ...item, enabled } : item,
                  ),
                })
              }
            />
            <Menu
              onDelete={() =>
                void save({
                  agentSkills: settings.agentSkills.filter(
                    (item) => item.id !== skill.id,
                  ),
                })
              }
            />
          </div>
        ))}
        {!settings.agentSkills.length && !creating && (
          <TeamEmpty
            icon={<Sparkles size={24} />}
            title="No agent skills"
            description="Add reusable instructions for agents working with this team."
          />
        )}
      </div>
    </TeamSection>
  );
}

function ProjectUpdatePromptSettings({
  data,
  team,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const { formatNumber, t } = useI18n();
  const { settings, setSettings, save } = useTeamSettings(data, team, onReload);
  return (
    <TeamSection title="Project update prompt">
      <div className="team-prompt-editor">
        <p>
          {t("Guidance used when generating project updates for")}{" "}
          <span data-i18n-ignore>{team.name}</span>.
        </p>
        <textarea
          aria-label="Project update prompt"
          value={settings.projectUpdatePrompt}
          onChange={(event) =>
            setSettings({
              ...settings,
              projectUpdatePrompt: event.target.value,
            })
          }
          onBlur={() =>
            void save({ projectUpdatePrompt: settings.projectUpdatePrompt })
          }
        />
        <span>
          {t(`${formatNumber(settings.projectUpdatePrompt.length)} characters`)}
        </span>
      </div>
    </TeamSection>
  );
}

function ResolvedSummariesSettings({
  data,
  team,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const { settings, save } = useTeamSettings(data, team, onReload);
  return (
    <TeamSection>
      <ToggleRow
        title="Resolved thread summaries"
        description="Generate a concise summary when a project comment thread is resolved."
        checked={settings.resolvedThreadSummaries}
        onChange={(value) => save({ resolvedThreadSummaries: value })}
      />
    </TeamSection>
  );
}

function MembersSettings({
  data,
  team,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const memberships = new Map(
    data.teamMembers
      .filter((item) => item.teamId === team.id)
      .map((item) => [item.userId, item]),
  );
  const change = async (userId: string, member: boolean) => {
    try {
      await setTeamMembership(
        data.workspace.urlKey,
        team.id,
        userId,
        member,
        memberships.get(userId)?.role ?? "member",
      );
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
  };
  return (
    <TeamSection title="Team members">
      <div className="team-setting-list">
        {data.members
          .filter((item) => item.status === "active")
          .map((member) => (
            <div className="team-member-setting" key={member.user.id}>
              <span className="settings-member-avatar">
                <span data-i18n-ignore>
                  {initials(member.user.displayName)}
                </span>
              </span>
              <span data-i18n-ignore>
                <strong>{member.user.displayName}</strong>
                <small>{member.user.email}</small>
              </span>
              {memberships.get(member.user.id)?.role === "owner" && (
                <em>Owner</em>
              )}
              <button
                role="checkbox"
                aria-checked={memberships.has(member.user.id)}
                onClick={() =>
                  void change(member.user.id, !memberships.has(member.user.id))
                }
              >
                {memberships.has(member.user.id) && <CheckboxMark />}
              </button>
            </div>
          ))}
      </div>
    </TeamSection>
  );
}

function LabelsSettings({
  data,
  team,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const labels = data.labels.filter(
    (label) => label.scope === team.id && !label.archivedAt,
  );
  const [creating, setCreating] = useState(false);
  const add = async (name: string, color: string) => {
    try {
      await createTeamLabel(team.id, { name, color });
      setCreating(false);
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
  };
  return (
    <TeamSection
      title="Issue labels"
      action={
        <button className="settings-action" onClick={() => setCreating(true)}>
          <Plus size={13} />
          New label
        </button>
      }
    >
      <div className="team-setting-list">
        {creating && (
          <InlineCreate
            placeholder="Label name"
            onCancel={() => setCreating(false)}
            onCreate={add}
          />
        )}{" "}
        {labels.map((label) => (
          <TeamLabelRow
            key={label.id}
            team={team}
            label={label}
            onReload={onReload}
          />
        ))}{" "}
        {!labels.length && !creating && (
          <TeamEmpty
            icon={<Circle size={22} />}
            title="No team labels"
            description="Team labels are available only on issues in this team."
          />
        )}
      </div>
    </TeamSection>
  );
}
function TeamLabelRow({
  team,
  label,
  onReload,
}: {
  team: Team;
  label: IssueLabel;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [name, setName] = useState(label.name);
  return (
    <div className="team-label-setting">
      <input
        type="color"
        value={label.color}
        aria-label={t("Label color")}
        onChange={(event) =>
          void updateTeamLabel(team.id, label.id, {
            color: event.target.value,
          }).then(onReload)
        }
      />
      <input
        value={name}
        onChange={(event) => setName(event.target.value)}
        onBlur={() =>
          name !== label.name &&
          void updateTeamLabel(team.id, label.id, { name }).then(onReload)
        }
      />
      <span data-i18n-ignore={Boolean(label.description) || undefined}>
        {label.description || "No description"}
      </span>
      <Menu
        onDelete={() => void deleteTeamLabel(team.id, label.id).then(onReload)}
      />
    </div>
  );
}

function TemplatesSettings({
  data,
  team,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [type, setType] = useState<"issue" | "project" | "document">("issue");
  const [editing, setEditing] = useState<
    IssueTemplate | ProjectTemplate | DocumentTemplate | null | undefined
  >(undefined);
  const templates =
    type === "issue"
      ? data.issueTemplates.filter((item) => item.teamId === team.id)
      : type === "project"
        ? data.projectTemplates.filter(
            (item) =>
              item.visibility === "teams" && item.teamIds.includes(team.id),
          )
        : data.documentTemplates.filter((item) => item.teamId === team.id);
  return (
    <>
      <div className="settings-segmented team-template-tabs">
        {(["issue", "project", "document"] as const).map((value) => (
          <button
            key={value}
            className={type === value ? "active" : ""}
            onClick={() => {
              setType(value);
              setEditing(undefined);
            }}
          >{t(`${titleCase(value)} templates`)}</button>
        ))}
      </div>
      <TeamSection
        title={`${titleCase(type)} templates`}
        action={
          <button className="settings-action" onClick={() => setEditing(null)}>
            <Plus size={13} />
            New template
          </button>
        }
      >
        <div className="team-setting-list">
          {templates.map((template) => (
            <button
              className="team-template-setting"
              key={template.id}
              onClick={() => setEditing(template)}
            >
              <span className="template-icon">T</span>
              <span>
                <strong data-i18n-ignore>{template.name}</strong>
                <small>
                  {template.description ? (
                    <span data-i18n-ignore>{template.description}</span>
                  ) : (
                    `${titleCase(type)} template`
                  )}
                </small>
              </span>
            </button>
          ))}
          {!templates.length && (
            <TeamEmpty
              icon={<Plus size={22} />}
              title={`No ${type} templates`}
              description={`Templates prefill common ${type} properties and descriptions.`}
            />
          )}
        </div>
      </TeamSection>
      {editing !== undefined &&
        (type === "document" ? (
          <DocumentTemplateEditor
            team={team}
            template={editing as DocumentTemplate | null}
            onClose={() => setEditing(undefined)}
            onSaved={onReload}
          />
        ) : (
          <TemplateEditor
            data={data}
            type={type}
            teamId={team.id}
            template={editing as IssueTemplate | ProjectTemplate | null}
            onClose={() => setEditing(undefined)}
            onSaved={onReload}
          />
        ))}
    </>
  );
}

function DocumentTemplateEditor({
  team,
  template,
  onClose,
  onSaved,
}: {
  team: Team;
  template: DocumentTemplate | null;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [name, setName] = useState(template?.name ?? "");
  const [description, setDescription] = useState(template?.description ?? "");
  const [documentTitle, setDocumentTitle] = useState(template?.title ?? "");
  const [content, setContent] = useState(template?.content ?? "");
  const [saving, setSaving] = useState(false);
  const save = async () => {
    setSaving(true);
    try {
      const input = {
        teamId: team.id,
        name,
        description,
        title: documentTitle,
        content,
      };
      if (template) await updateDocumentTemplate(template.id, input);
      else await createDocumentTemplate(input);
      await onSaved();
      onClose();
    } catch (error) {
      toast.error(message(error));
    } finally {
      setSaving(false);
    }
  };
  const remove = async () => {
    if (!template) return;
    try {
      await deleteDocumentTemplate(template.id);
      await onSaved();
      onClose();
    } catch (error) {
      toast.error(message(error));
    }
  };
  return (
    <div className="template-full-editor">
      <header>
        <button onClick={onClose}>{t("Cancel")}</button>
        <strong>
          {t(template ? "Edit document template" : "New document template")}
        </strong>
        <button
          className="primary"
          disabled={!name.trim() || saving}
          onClick={() => void save()}
        >
          {t(saving ? "Saving…" : "Save")}
        </button>
      </header>
      <div className="template-editor-content">
        <label>
          {t("Template name")}
          <input
            autoFocus
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label>
          {t("Template description")}
          <input
            value={description}
            onChange={(event) => setDescription(event.target.value)}
          />
        </label>
        <label>
          {t("Document title")}
          <input
            value={documentTitle}
            onChange={(event) => setDocumentTitle(event.target.value)}
          />
        </label>
        <label>
          {t("Document content")}
          <textarea
            rows={14}
            value={content}
            onChange={(event) => setContent(event.target.value)}
          />
        </label>
        {template && (
          <button
            className="settings-action danger"
            onClick={() => void remove()}
          >
            <Trash2 size={14} />
            {t("Delete template")}
          </button>
        )}
      </div>
    </div>
  );
}

function StatusesSettings({
  data,
  team,
  onBack,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onBack: () => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [states, setStates] = useState<WorkflowState[]>(
    statesForTeam(data, team.id),
  );
  const [createType, setCreateType] = useState<WorkflowStateType | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [dragging, setDragging] = useState<string | null>(null);
  useEffect(() => {
    void fetchWorkflowStates(team.id)
      .then(setStates)
      .catch(() => setStates(statesForTeam(data, team.id)));
  }, [data, team.id]);
  const reload = async () => {
    setStates(await fetchWorkflowStates(team.id));
    await onReload();
  };
  const run = async (action: () => Promise<unknown>) => {
    try {
      await action();
      await reload();
    } catch (error) {
      toast.error(message(error));
    }
  };
  const move = (state: WorkflowState, delta: number) => {
    const group = states.filter(
      (item) => item.type === state.type && !item.reserved,
    );
    const index = group.findIndex((item) => item.id === state.id),
      target = index + delta;
    if (target < 0 || target >= group.length) return;
    const next = [...states],
      left = next.findIndex((item) => item.id === group[index].id),
      right = next.findIndex((item) => item.id === group[target].id);
    [next[left], next[right]] = [next[right], next[left]];
    setStates(next);
    void run(() =>
      reorderWorkflowStates(
        team.id,
        next.map((item) => item.id),
      ),
    );
  };
  const drop = (target: WorkflowState) => {
    const source = states.find((item) => item.id === dragging);
    setDragging(null);
    if (
      !source ||
      source.id === target.id ||
      source.type !== target.type ||
      source.reserved ||
      target.reserved
    )
      return;
    const next = states.filter((item) => item.id !== source.id),
      targetIndex = next.findIndex((item) => item.id === target.id);
    next.splice(targetIndex, 0, source);
    setStates(next);
    void run(() =>
      reorderWorkflowStates(
        team.id,
        next.map((item) => item.id),
      ),
    );
  };
  const busy = createType !== null || editing !== null;
  return (
    <div
      className="ip-settings-page ip-project-statuses-page team-statuses-page"
      data-i18n-ignore
    >
      <header className="settings-page-header ip-page-header team-statuses-header">
        <button aria-label={t("Back to team settings")} onClick={onBack}>
          <ArrowLeft size={14} />
          <span data-i18n-ignore>{team.name}</span>
        </button>
        <div>
          <h1>{t("Issue statuses")}</h1>
          <p>
            {t(
              "Issue statuses define the workflow that issues go through from start to completion.",
            )}
          </p>
        </div>
      </header>
      <section
        className="ip-status-card"
        role="list"
        aria-label={t("Issue statuses")}
      >
        {STATUS_GROUPS.map((group) => {
          const groupStates = states.filter(
              (item) =>
                item.type === group.type &&
                Boolean(item.reserved) === Boolean(group.reserved),
            ),
            canModify = !group.reserved && groupStates.length > 1;
          return (
            <div className="ip-status-section" role="list" key={group.label}>
              <header>
                <h3>{t(group.label)}</h3>
                {!group.reserved && (
                  <button
                    aria-label={t("Create new workflow state")}
                    disabled={busy}
                    onClick={() => setCreateType(group.type)}
                  >
                    <Plus />
                  </button>
                )}
              </header>
              {groupStates.map((state) => {
                const usage = data.issues.filter(
                  (issue) =>
                    issue.team.id === team.id && issue.state.id === state.id,
                ).length;
                return editing === state.id ? (
                  <IssueStateEditor
                    key={state.id}
                    state={state}
                    onCancel={() => setEditing(null)}
                    onSave={async (input) => {
                      await run(() =>
                        updateWorkflowState(team.id, state.id, input),
                      );
                      setEditing(null);
                    }}
                  />
                ) : (
                  <IssueStateRow
                    key={state.id}
                    state={state}
                    usage={usage}
                    canModify={canModify}
                    dragging={dragging === state.id}
                    workspaceKey={data.workspace.urlKey}
                    teamKey={team.key}
                    onDragStart={() => setDragging(state.id)}
                    onDragEnd={() => setDragging(null)}
                    onDrop={() => drop(state)}
                    onMove={(delta) => move(state, delta)}
                    onEdit={() => setEditing(state.id)}
                    onDelete={() => {
                      if (!canModify) {
                        toast(t("Can't delete status"), {
                          description: t(
                            "You can't delete the last status of a type.",
                          ),
                        });
                        return;
                      }
                      if (usage) {
                        toast(`Can't delete the "${state.name}" issue status`, {
                          description: `The status has ${usage} ${usage === 1 ? "issue" : "issues"} assigned. Please archive or move them before deleting the status.`,
                        });
                        return;
                      }
                      void run(() => deleteWorkflowState(team.id, state.id));
                    }}
                  />
                );
              })}
              {!group.reserved && createType === group.type && (
                <IssueStateEditor
                  type={group.type}
                  onCancel={() => setCreateType(null)}
                  onSave={async (input) => {
                    await run(() => createWorkflowState(team.id, input));
                    setCreateType(null);
                  }}
                />
              )}
            </div>
          );
        })}
      </section>
    </div>
  );
}

function IssueStateEditor({
  type,
  state,
  onCancel,
  onSave,
}: {
  type?: WorkflowStateType;
  state?: WorkflowState;
  onCancel: () => void;
  onSave: (input: {
    name: string;
    description: string;
    color: string;
    type: WorkflowStateType;
  }) => Promise<void>;
}) {
  const { t } = useI18n(),
    stateType = state?.type ?? type ?? "unstarted",
    [name, setName] = useState(state?.name ?? ""),
    [description, setDescription] = useState(state?.description ?? ""),
    [color, setColor] = useState(
      state?.color ?? ISSUE_STATUS_COLORS[stateType],
    ),
    [saving, setSaving] = useState(false);
  const submit = async () => {
    if (!name.trim()) return;
    setSaving(true);
    try {
      await onSave({
        name: name.trim(),
        description: description.trim(),
        color,
        type: stateType,
      });
    } finally {
      setSaving(false);
    }
  };
  const preview = {
    id: "status-color-preview",
    name: name || "Status",
    color,
    type: stateType,
    position: 0,
  };
  return (
    <form
      className="ip-status-editor"
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape") onCancel();
      }}
    >
      <StatusDragHandle />
      <StatusColorPicker
        color={color}
        type={stateType}
        preview={<StatusIcon state={preview} size={16} />}
        onChange={setColor}
      />
      <input
        autoFocus
        required
        aria-label={t("Name")}
        placeholder={t("Name")}
        value={name}
        onChange={(event) => setName(event.target.value)}
      />
      <input
        aria-label={t("Description")}
        placeholder={t("Description…")}
        value={description}
        onChange={(event) => setDescription(event.target.value)}
      />
      <footer>
        <button aria-label={t("Cancel")} type="button" onClick={onCancel}>
          {t("Cancel")}
        </button>
        <button
          aria-label={t("Submit")}
          className="primary"
          disabled={saving || !name.trim()}
        >
          {t(state ? "Save" : "Create")}
        </button>
      </footer>
    </form>
  );
}

function IssueStateRow({
  state,
  usage,
  canModify,
  dragging,
  workspaceKey,
  teamKey,
  onDragStart,
  onDragEnd,
  onDrop,
  onMove,
  onEdit,
  onDelete,
}: {
  state: WorkflowState;
  usage: number;
  canModify: boolean;
  dragging: boolean;
  workspaceKey: string;
  teamKey: string;
  onDragStart: () => void;
  onDragEnd: () => void;
  onDrop: () => void;
  onMove: (delta: number) => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const { t } = useI18n(),
    [menuOpen, setMenuOpen] = useState(false);
  const view = () => {
    location.href = `/${encodeURIComponent(workspaceKey)}/team/${encodeURIComponent(teamKey)}/all?status=${encodeURIComponent(state.id)}`;
  };
  const menuKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const items = [
        ...event.currentTarget.querySelectorAll<HTMLButtonElement>(
          "[role=option]",
        ),
      ],
      index = items.indexOf(document.activeElement as HTMLButtonElement);
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      items[
        (index + (event.key === "ArrowDown" ? 1 : -1) + items.length) %
          items.length
      ]?.focus();
    }
    if (event.key === "Home") {
      event.preventDefault();
      items[0]?.focus();
    }
    if (event.key === "End") {
      event.preventDefault();
      items.at(-1)?.focus();
    }
  };
  const usageControl =
    usage > 0 ? (
      canModify ? (
        <button
          className="ip-status-usage"
          aria-label={t("View issues")}
          onClick={(event) => {
            event.stopPropagation();
            view();
          }}
        >
          {usage} {t(usage === 1 ? "issue" : "issues")}
        </button>
      ) : (
        <span className="ip-status-usage is-disabled">
          {usage} {t(usage === 1 ? "issue" : "issues")}
        </span>
      )
    ) : null;
  return (
    <div
      className={`ip-status-row${dragging ? " is-dragging" : ""}`}
      role="button"
      aria-disabled={!canModify}
      aria-roledescription="sortable"
      tabIndex={canModify ? 0 : -1}
      draggable={canModify}
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onDragOver={(event) => {
        if (canModify) event.preventDefault();
      }}
      onDrop={(event) => {
        event.preventDefault();
        onDrop();
      }}
      onKeyDown={(event) => {
        if (!canModify || !event.altKey) return;
        if (event.key === "ArrowUp") {
          event.preventDefault();
          onMove(-1);
        }
        if (event.key === "ArrowDown") {
          event.preventDefault();
          onMove(1);
        }
      }}
    >
      {canModify && <StatusDragHandle />}
      <span
        className="issue-status-mark"
        style={{ "--status-color": state.color } as React.CSSProperties}
      >
        <StatusIcon state={state} size={16} />
      </span>
      <span className="ip-status-copy">
        <strong data-i18n-ignore>
          {state.name}
          {state.default ? (
            <>
              {" · "}
              <b>{t("Default")}</b>
            </>
          ) : null}
        </strong>
        {usageControl || state.description ? (
          <small>
            {usageControl}
            {usageControl && state.description ? " · " : null}
            {state.description ? (
              <span data-i18n-ignore>{state.description}</span>
            ) : null}
          </small>
        ) : null}
      </span>
      {canModify ? (
        <Popover.Root open={menuOpen} onOpenChange={setMenuOpen}>
          <Popover.Trigger asChild>
            <button className="ip-status-more" aria-label={t("Open menu")}>
              <MoreHorizontal />
            </button>
          </Popover.Trigger>
          <Popover.Portal>
            <Popover.Content
              className="ip-status-menu"
              align="end"
              sideOffset={4}
              data-i18n-ignore
              onKeyDown={menuKeyDown}
            >
              <span className="ip-status-menu-filter">
                <input autoFocus aria-label={t("Filter…")} tabIndex={-1} />
              </span>
              <button
                role="option"
                onClick={() => {
                  setMenuOpen(false);
                  onEdit();
                }}
              >
                <StatusMenuIcon name="edit" />
                <span>{t("Edit")}</span>
              </button>
              <button
                role="option"
                onClick={() => {
                  setMenuOpen(false);
                  onDelete();
                }}
              >
                <StatusMenuIcon name="delete" />
                <span>{t("Delete")}</span>
              </button>
            </Popover.Content>
          </Popover.Portal>
        </Popover.Root>
      ) : (
        <span />
      )}
    </div>
  );
}

const ISSUE_STATUS_COLORS: Record<WorkflowStateType, string> = {
  backlog: "#6b6f76",
  unstarted: "#a8a8aa",
  started: "#e2b714",
  completed: "#5e6ad2",
  canceled: "#8a8f98",
};

function CyclesSettings({
  data,
  team,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const initial = data.cycleSettings[team.id] ?? defaultCycles();
  const [settings, setSettings] = useState(initial);
  useEffect(
    () => setSettings(data.cycleSettings[team.id] ?? defaultCycles()),
    [data.cycleSettings, team.id],
  );
  const save = async (patch: Partial<CycleSettings>) => {
    const optimistic = { ...settings, ...patch };
    setSettings(optimistic);
    try {
      setSettings(await updateCycleSettings(team.id, patch));
      await onReload();
    } catch (error) {
      setSettings(settings);
      toast.error(message(error));
    }
  };
  return (
    <>
      <TeamSection>
        <ToggleRow
          title="Enable cycles"
          description="Organize work into time-boxed periods."
          checked={settings.enabled}
          onChange={(value) => save({ enabled: value })}
        />
      </TeamSection>
      <div className={!settings.enabled ? "settings-disabled-area" : ""}>
        <TeamSection title="Schedule">
          <SelectRow
            title="Cycle duration"
            value={String(settings.durationWeeks)}
            options={["1", "2", "3", "4", "6", "8"]}
            labels={Object.fromEntries(
              ["1", "2", "3", "4", "6", "8"].map((value) => [
                value,
                `${value} week${value === "1" ? "" : "s"}`,
              ]),
            )}
            onChange={(value) => save({ durationWeeks: Number(value) })}
          />
          <SelectRow
            title="Cooldown"
            value={String(settings.cooldownWeeks)}
            options={["0", "1", "2", "3", "4"]}
            labels={Object.fromEntries(
              ["0", "1", "2", "3", "4"].map((value) => [
                value,
                value === "0"
                  ? "No cooldown"
                  : `${value} week${value === "1" ? "" : "s"}`,
              ]),
            )}
            onChange={(value) => save({ cooldownWeeks: Number(value) })}
          />
          <NumberRow
            title="Capacity"
            description="Planning capacity shown on each generated cycle."
            value={settings.capacity}
            onCommit={(value) => save({ capacity: value })}
          />
          <NumberRow
            title="Upcoming cycles"
            value={settings.upcomingCount}
            onCommit={(value) => save({ upcomingCount: value })}
          />
        </TeamSection>
        <TeamSection title="Automations">
          <ToggleRow
            title="Automatically create upcoming cycles"
            checked={settings.autoCreate}
            onChange={(value) => save({ autoCreate: value })}
          />
          <ToggleRow
            title="Add active issues"
            checked={settings.autoAddActive}
            onChange={(value) => save({ autoAddActive: value })}
          />
          <ToggleRow
            title="Add issues with due dates in the cycle"
            checked={settings.autoAddDueDate}
            onChange={(value) => save({ autoAddDueDate: value })}
          />
          <ToggleRow
            title="Add started issues"
            checked={settings.autoAddStarted}
            onChange={(value) => save({ autoAddStarted: value })}
          />
          <ToggleRow
            title="Add completed issues"
            checked={settings.autoAddCompleted}
            onChange={(value) => save({ autoAddCompleted: value })}
          />
          <ToggleRow
            title="Move unfinished issues to the next cycle"
            checked={settings.autoMigrate}
            onChange={(value) => save({ autoMigrate: value })}
          />
        </TeamSection>
      </div>
    </>
  );
}

function TeamSection({
  title,
  description,
  action,
  children,
}: {
  title?: string;
  description?: React.ReactNode;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  const { t } = useI18n();
  return (
    <SettingsSection
      action={action}
      description={
        typeof description === "string" ? t(description) : description
      }
      headerClassName="team-settings-section-title"
      title={title ? t(title) : title}
    >
      {children}
    </SettingsSection>
  );
}
function TeamRow(props: React.ComponentProps<typeof SettingsRow>) {
  const { t } = useI18n();
  return (
    <SettingsRow
      {...props}
      title={typeof props.title === "string" ? t(props.title) : props.title}
      description={
        typeof props.description === "string"
          ? t(props.description)
          : props.description
      }
    />
  );
}
function SettingsSelect({
  entityName,
  label,
  options,
  ...props
}: Omit<React.ComponentProps<typeof BaseSettingsSelect>, "label" | "options"> & {
  label: string;
  options: SettingsSelectOption[];
}) {
  const { t } = useI18n();
  return (
    <BaseSettingsSelect
      {...props}
      entityName={entityName}
      label={t(label)}
      options={options.map((option) => {
        if (typeof option === "string") {
          const isEntity = entityName?.(option) ?? false;
          return {
            value: option,
            label: isEntity ? option : t(option),
            entityName: isEntity,
          };
        }
        return option.entityName
          ? option
          : { ...option, label: t(option.label) };
      })}
    />
  );
}
function InputRow({
  title,
  description,
  value,
  onChange,
  onCommit,
}: {
  title: string;
  description?: string;
  value: string;
  onChange: (value: string) => void;
  onCommit: (value: string) => void | Promise<void>;
}) {
  const { t } = useI18n();
  const initialOnFocus = useRef(value);
  return (
    <TeamRow title={title} description={description}>
      <input
        aria-label={t(title)}
        className="settings-input"
        value={value}
        onFocus={() => {
          initialOnFocus.current = value;
        }}
        onChange={(event) => onChange(event.target.value)}
        onBlur={() => {
          if (value !== initialOnFocus.current) void onCommit(value);
        }}
      />
    </TeamRow>
  );
}
function SelectRow({
  title,
  description,
  value,
  options,
  labels = {},
  entityOptions = [],
  onChange,
}: {
  title: string;
  description?: string;
  value: string;
  options: string[];
  labels?: Record<string, string>;
  entityOptions?: string[];
  onChange: (value: string) => void | Promise<void>;
}) {
  const { t } = useI18n();
  return (
    <TeamRow title={title} description={description}>
      <SettingsSelect
        label={t(title)}
        value={value}
        onChange={(next) => void onChange(next)}
        options={options.map((option) => ({
          value: option,
          label: entityOptions.includes(option)
            ? labels[option] ?? option
            : t(labels[option] ?? option),
          entityName: entityOptions.includes(option),
        }))}
      />
    </TeamRow>
  );
}
function ToggleRow({
  title,
  description,
  checked,
  onChange,
}: {
  title: string;
  description?: string;
  checked: boolean;
  onChange: (value: boolean) => void | Promise<void>;
}) {
  const { t } = useI18n();
  return (
    <TeamRow title={title} description={description}>
      <SettingsToggle checked={checked} label={t(title)} onChange={onChange} />
    </TeamRow>
  );
}
function NumberRow({
  title,
  description,
  value,
  onCommit,
}: {
  title: string;
  description?: string;
  value: number;
  onCommit: (value: number) => void | Promise<void>;
}) {
  const { t } = useI18n();
  const [draft, setDraft] = useState(String(value));
  useEffect(() => setDraft(String(value)), [value]);
  return (
    <TeamRow title={title} description={description}>
      <input
        aria-label={t(title)}
        type="number"
        min={0}
        className="settings-input short"
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={() => {
          const next = Number(draft);
          if (next !== value) void onCommit(next);
        }}
      />
    </TeamRow>
  );
}
function InlineCreate({
  placeholder,
  onCancel,
  onCreate,
}: {
  placeholder: string;
  onCancel: () => void;
  onCreate: (name: string, color: string) => void | Promise<void>;
}) {
  const { t } = useI18n();
  const [name, setName] = useState("");
  const [color, setColor] = useState("#5E6AD2");
  return (
    <form
      className="workflow-inline-create"
      onSubmit={(event) => {
        event.preventDefault();
        if (name.trim()) void onCreate(name.trim(), color);
      }}
    >
      <input
        type="color"
        value={color}
        onChange={(event) => setColor(event.target.value)}
      />
      <input
        autoFocus
        placeholder={t(placeholder)}
        value={name}
        onChange={(event) => setName(event.target.value)}
      />
      <button type="button" aria-label={t("Cancel")} onClick={onCancel}>
        <X size={14} />
      </button>
      <button className="create" disabled={!name.trim()}>
        <Check size={14} />
      </button>
    </form>
  );
}
function Menu({ onDelete }: { onDelete: () => void }) {
  const { t } = useI18n();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button className="settings-icon-action" aria-label={t("More actions")}>
          <MoreHorizontal size={15} />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem className="danger-item" onSelect={onDelete}>
          <Trash2 size={14} />
          {t("Delete")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
function TeamEmpty({
  icon,
  title,
  description,
}: {
  icon: React.ReactNode;
  title: string;
  description: string;
}) {
  const { t } = useI18n();
  return (
    <div className="settings-empty compact">
      {icon}
      <h3>{t(title)}</h3>
      <p>{t(description)}</p>
    </div>
  );
}
function statesForTeam(data: BootstrapData, teamId: string) {
  const specific = data.states.some((state) => state.teamId === teamId);
  return data.states
    .filter((state) => (specific ? state.teamId === teamId : !state.teamId))
    .sort((a, b) => a.position - b.position);
}
function defaultTeamSettings(
  teamId: string,
  states: WorkflowState[],
): TeamSettings {
  return {
    teamId,
    timezone: "Etc/UTC",
    estimateType: "notUsed",
    defaultStateId:
      states.find((state) => state.default)?.id ?? states[0]?.id ?? "",
    defaultPriority: 0,
    issueEmailEnabled: false,
    detailedHistory: false,
    access: "public",
    membershipRestriction: "open",
    settingsPermission: "allMembers",
    labelPermission: "allMembers",
    templatePermission: "allMembers",
    agentSkillPermission: "allMembers",
    loopPermission: "allMembers",
    memberPermission: "allMembers",
    slackNotifications: {},
    prAutomations: {},
    autoCloseParents: false,
    autoCloseSubIssues: false,
    autoCloseStale: false,
    staleMonths: 6,
    autoArchiveMonths: 6,
    progressOrder: "first",
    releaseAutomations: [],
    triageEnabled: false,
    triageRequirePriority: false,
    triageAction: "none",
    triageRules: [],
    agentSkills: [],
    projectUpdatePrompt: "",
    resolvedThreadSummaries: true,
    showInitiatives: true,
  };
}
function defaultCycles(): CycleSettings {
  return {
    enabled: false,
    durationWeeks: 2,
    cooldownWeeks: 0,
    startsOn: 1,
    upcomingCount: 2,
    capacity: 4,
    autoCreate: true,
    autoAddActive: false,
    autoAddDueDate: false,
    autoAddStarted: false,
    autoAddCompleted: false,
    autoMigrate: true,
    favoriteView: false,
  };
}
function initials(value: string) {
  return value
    .split(/\s+/)
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}
function message(error: unknown) {
  return error instanceof Error
    ? error.message
    : "Could not save team settings";
}
function titleCase(value: string) {
  return value.slice(0, 1).toUpperCase() + value.slice(1);
}
