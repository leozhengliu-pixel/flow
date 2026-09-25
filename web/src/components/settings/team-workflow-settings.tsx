import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ComponentType,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import * as Popover from "@radix-ui/react-popover";
import {
  Bot,
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
import { ParentTeamPicker } from '@/components/property/parent-team-picker';
import { RetireTeamForm } from '@/components/team/retire-team-form';
import { DELETE_TEAM_DESCRIPTION } from '@/lib/team-deletion';
import { teamHierarchy } from '@/lib/team-hierarchy';

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
  createTriageResponsibility,
  createTriageRule,
  createWorkflowState,
  deleteDocumentTemplate,
  deleteEmailIntakeAddress,
  deleteGitAutomation,
  deleteTargetBranch,
  deleteTeam,
  deleteTeamCycles,
  deleteTriageResponsibility,
  deleteTriageRule,
  fetchWorkflowStates,
  listIssueRecordGroups,
  reorderWorkflowStates,
  setTeamMembership,
  updateCycleSettings,
  rotateEmailIntakeAddress,
  updateDocumentTemplate,
  updateIssue,
  updateStructuredTeamSettings,
  updateTeam,
  updateWorkflowState,
  upsertGitAutomation,
  upsertTargetBranch,
} from "@/lib/api";
import type {
  BootstrapData,
  CycleSettings,
  DocumentTemplate,
  IssueTemplate,
  ProjectTemplate,
  Team,
  TeamSettings,
  WorkflowState,
  WorkflowStateType,
} from "@/types/flow";
import { loopsPath, settingsPath, type TeamSettingsSection } from "@/lib/app-routes";
import { useNavigate } from "react-router-dom";
import {
  confirmAllowSubTeamsMembership,
  confirmApplyPermissionToSubTeams,
  getIssueSharingAudience,
  isLessRestrictiveMembership,
  isLessRestrictivePermission,
  type TeamPermissionKey,
} from "@/lib/team-security-confirms";

import { TemplateEditor } from "./advanced-settings";
import {
  SettingsCrumb,
  SettingsRow,
  SettingsSection,
  SettingsSelect as BaseSettingsSelect,
  SettingsToggle,
  TeamSettingsCrumb,
  type SettingsSelectOption,
} from "./settings-primitives";
import { DomainLabelsSettings } from "./domain-settings";
import { ESTIMATE_TYPE_NAMES, estimateScaleDetails } from "@/lib/estimates";
import { TeamMembersSettings } from "./team-members-settings";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { StatusIcon } from "@/components/issue/issue-icons";
import { DefaultFavoritesSettings } from './team-default-favorites-settings';
import { TeamProjectStatusesSettingsPage } from './team-project-statuses-settings';
import { PropertyMenu } from '@/components/property/property-menu';
import { WorkflowStateDeleteDialog } from './workflow-state-delete-dialog';
import {
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
    id: "project-labels",
    label: "Project labels",
    description: "Labels available to this team’s projects",
  },
  {
    id: "default-favorites",
    label: "Default favorites",
    description: "Resources that appear in Favorites for every team member",
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
    id: "project-statuses",
    label: "Project statuses",
    description: "Customize the statuses projects go through, or inherit from parent/workspace",
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
    id: "agent-connectors",
    label: "Agent connectors",
    description: "Add MCP connectors that team members can use with Flow Agent",
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
/** Page descriptions shown under the title. */
const TEAM_SECTION_DESCRIPTIONS: Partial<Record<TeamSettingsSection, string>> = {
  cycles:
    "Cycles create rhythm and focus with short, time-boxed planning windows. Automations can create future cycles, carry over unfinished work, and move issues in or out based on status.",
  workflow: "Manage issue automations, git workflows and other workflows",
  triage: "Define how incoming issues and requests are handled in triage",
  notifications: "Connect a Slack channel to receive notifications when issues are created or updated in this team.",
  templates: "Any templates created here will be available when creating issues, projects, and documents within this team. To create templates that apply to all teams, do so in the workspace issue, project, or document templates sections.",
};
/** Page titles where they differ from the overview row label. */
const TEAM_SECTION_TITLES: Partial<Record<TeamSettingsSection, string>> = {
  members: "Team members",
  "issue-labels": "Team issue labels",
  "project-labels": "Team project labels",
  templates: "Team templates",
};
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
  onOpenTeams,
  onReload,
  subPath,
}: {
  data: BootstrapData;
  team: Team;
  section: TeamSettingsSection;
  onNavigate: (section: TeamSettingsSection, subPath?: string) => void;
  onOpenTeams?: () => void;
  onReload: () => Promise<void>;
  /** Sub-page within the section, e.g. "issue/new" under templates. */
  subPath?: string;
}) {
  const { t } = useI18n();
  if (section === "statuses")
    return (
      <StatusesSettings
        key={team.id}
        data={data}
        team={team}
        onBack={() => onNavigate("overview")}
        onReload={onReload}
      />
    );
  if (section === "members")
    return (
      <>
        <TeamSettingsCrumb team={team} onClick={() => onNavigate("overview")} />
        <TeamMembersSettings key={team.id} data={data} team={team} onReload={onReload} />
      </>
    );
  if (section === "issue-labels" || section === "project-labels")
    return (
      <>
        <TeamSettingsCrumb team={team} onClick={() => onNavigate("overview")} />
        <DomainLabelsSettings
          key={`${team.id}:${section}`}
          data={data}
          team={team}
          resourceType={section === "issue-labels" ? "issue" : "project"}
          onReload={onReload}
        />
      </>
    );
  if (section === "project-statuses")
    return (
      <TeamProjectStatusesSettingsPage
        key={team.id}
        data={data}
        team={team}
        onBack={() => onNavigate("overview")}
        onReload={onReload}
      />
    );
  const Content = TEAM_SECTION_COMPONENTS[section];
  return (
    <>
      {section === "overview" ? (
        onOpenTeams && <SettingsCrumb onClick={onOpenTeams}>{t("Teams")}</SettingsCrumb>
      ) : (
        <TeamSettingsCrumb team={team} onClick={() => onNavigate("overview")} />
      )}
      <header className="settings-page-header team-settings-header">
        <div>
          <h1>
            {section === "overview" ? (
              <span data-i18n-ignore>{team.name}</span>
            ) : (
              t(TEAM_SECTION_TITLES[section] ?? SECTIONS.find((item) => item.id === section)?.label ?? "")
            )}
          </h1>
          {TEAM_SECTION_DESCRIPTIONS[section] && <p>{t(TEAM_SECTION_DESCRIPTIONS[section])}</p>}
        </div>
      </header>
      {section === "overview" && (
        <TeamOverview
          data={data}
          team={team}
          onNavigate={onNavigate}
          onReload={onReload}
          action={subPath}
          onActionHandled={() => onNavigate("overview")}
          onOpenTeams={onOpenTeams}
        />
      )}
  {section === "agents" && <TeamAgentsSettings data={data} />}
      {section === "agent-connectors" && (
        <TeamAgentConnectorsSettings data={data} team={team} onReload={onReload} />
      )}
      {Content && (
        <Content
          data={data}
          team={team}
          onReload={onReload}
          subPath={subPath}
          onNavigateSubPath={(next) => onNavigate(section, next)}
        />
      )}
    </>
  );
}

type TeamSectionComponent = ComponentType<{
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
  subPath?: string;
  onNavigateSubPath?: (subPath?: string) => void;
}>;
const TEAM_SECTION_COMPONENTS: Partial<
  Record<TeamSettingsSection, TeamSectionComponent>
> = {
  general: GeneralSettings,
  security: AccessSettings,
  notifications: SlackSettings,
  templates: TemplatesSettings,
  "recurring-issues": RecurringIssuesSettings,
  workflow: WorkflowSettings,
  triage: TriageSettings,
  cycles: CyclesSettings,
  "agent-skills": AgentSkillsSettings,
  "default-favorites": DefaultFavoritesSettings,
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
  action,
  onActionHandled,
  onOpenTeams,
}: {
  data: BootstrapData;
  team: Team;
  onNavigate: (section: TeamSettingsSection) => void;
  onReload: () => Promise<void>;
  /** Deep-linked action: "retire", "set-parent", "change-parent" or "remove-parent". */
  action?: string;
  onActionHandled: () => void;
  onOpenTeams?: () => void;
}) {
  const { t } = useI18n();
  const { settings, save } = useTeamSettings(data, team, onReload);
  const [retireOpen, setRetireOpen] = useState(false);
  useEffect(() => {
    if (action === "retire" && !team.retiredAt) setRetireOpen(true);
  }, [action, team.retiredAt]);
  const parentAction = action === "set-parent" || action === "change-parent" ? "pick" : action === "remove-parent" ? "remove" : undefined;
  const descendantCount = Math.max(0, teamHierarchy(data.teams, data.teamSettings).subtree(team.id).size - 1);
  const retire = async () => {
    if (!team.retiredAt) {
      setRetireOpen(true);
      return;
    }
    if (
      !(await confirmAction(`${t("Restore")} ${team.name}?`, {
        confirmLabel: t("Restore team"),
        danger: false,
        description:
          descendantCount > 0
            ? `This team and ${descendantCount} nested sub-team${descendantCount === 1 ? "" : "s"} will be restored together.`
            : undefined,
      }))
    )
      return;
    try {
      await updateTeam(data.workspace.urlKey, team.id, {
        retired: false,
        subTeamAction: "retire",
      });
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
  };
  const remove = async () => {
    if (
      !(await confirmAction(`Delete ${team.name}?`, {
        description: t(DELETE_TEAM_DESCRIPTION),
        confirmLabel: t("Delete team"),
      }))
    )
      return;
    try {
      await deleteTeam(data.workspace.urlKey, team.id);
      toast.success(t("Team deleted"));
      onOpenTeams?.();
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
  };
  const leave = async () => {
    if (
      !(await confirmAction(`Leave ${team.name}?`, {
        confirmLabel: t("Leave team"),
      }))
    )
      return;
    try {
      await setTeamMembership(
        data.workspace.urlKey,
        team.id,
        data.viewer.id,
        false,
        "member",
      );
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
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
          {["agents", "agent-connectors", "agent-skills"].map((id) => {
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
        description="Organize teams into a hierarchy of up to five levels."
      >
        <ParentTeamPicker data={data} teams={data.teams} settings={data.teamSettings} teamId={team.id} value={settings.parentTeamId} action={parentAction} onActionHandled={onActionHandled} onChange={value => { void save({parentTeamId: value}) }}/>
      </TeamSection>
      <TeamSection title="Danger zone">
        <TeamRow
          title="Leave team"
          description="Remove yourself as a member of this team"
        >
          <button
            type="button"
            className="settings-action"
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
            type="button"
            className="settings-action"
            onClick={() => void retire()}
          >
            {team.retiredAt ? "Restore…" : "Retire…"}
          </button>
        </TeamRow>
        <TeamRow
          title="Delete team"
          description="Permanently delete this team and all its data, with a 30-day restoration window"
        >
          <button
            type="button"
            className="settings-action"
            disabled={data.teams.filter((item) => !item.retiredAt).length <= 1}
            onClick={() => void remove()}
          >
            Delete…
          </button>
        </TeamRow>
      </TeamSection>
      <RetireTeamForm
        open={retireOpen}
        onOpenChange={(open) => {
          setRetireOpen(open);
          if (!open && action === "retire") onActionHandled();
        }}
        data={data}
        team={team}
        onReload={onReload}
      />
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
      <TeamSection>
        <TeamRow title="Name">
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
              aria-label="Name"
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
          description="Used in issue IDs"
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
        title="Team initiatives"
        description="Choose whether initiatives led by this team appear in the team sidebar"
      >
        <ToggleRow
          title="Hide initiatives in the team sidebar"
          checked={settings.showInitiatives === false}
          onChange={(value) => save({ showInitiatives: !value })}
        />
      </TeamSection>
      <TeamSection title="Estimates" description="Used to estimate issue complexity and plan cycle capacity.">
        {settings.parentTeamId && <ToggleRow
          title="Inherit estimate settings from parent team"
          description="Keep these settings in sync with parent team"
          checked={settings.inheritIssueEstimation}
          onChange={(value) => save({ inheritIssueEstimation: value })}
        />}
        {settings.inheritIssueEstimation && settings.parentTeamId ? <TeamRow title="Issue estimation" description="This team is inheriting estimate settings from its parent team."><span className="team-inherited-value">{data.teams.find((item) => item.id === settings.parentTeamId)?.name}</span></TeamRow> : <SelectRow
          title="Issue estimation"
          value={settings.estimateType}
          options={["notUsed", "exponential", "fibonacci", "flow", "tShirt"]}
          labels={Object.fromEntries(
            (["notUsed", "exponential", "fibonacci", "flow", "tShirt"] as const).map((type) => [
              type,
              `${ESTIMATE_TYPE_NAMES[type]} ${estimateScaleDetails(type, settings.estimateAllowZero, settings.estimateExtended)}`.trim(),
            ]),
          )}
          onChange={(value) =>
            save({ estimateType: value as TeamSettings["estimateType"] })
          }
        />}
        {settings.estimateType !== "notUsed" && !(settings.inheritIssueEstimation && settings.parentTeamId) && (
          <>
            <ToggleRow
              title="Allow zero estimates"
              description="When enabled, issues can be estimated with zero points. This is useful if, for example, you don’t want to count parent issues towards the total estimate."
              checked={Boolean(settings.estimateAllowZero)}
              onChange={(value) => save({ estimateAllowZero: value })}
            />
            <ToggleRow
              title="Extended estimate scale"
              description="When enabled, the estimate scale is extended. This is normally not recommended, as large estimates usually mean that an issue should be broken up into smaller issues."
              checked={Boolean(settings.estimateExtended)}
              onChange={(value) => save({ estimateExtended: value })}
            />
            <ToggleRow
              title="Count unestimated issues"
              description="When enabled, issues that have not been estimated will count as 1 estimate point. When disabled, unestimated issues count as 0 estimate points."
              checked={settings.estimateCountUnestimated !== false}
              onChange={(value) => save({ estimateCountUnestimated: value })}
            />
          </>
        )}
      </TeamSection>
      <EmailIntakeSettings
        data={data}
        team={team}
        settings={settings}
        save={save}
        onReload={onReload}
      />
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
  const restrictedParent = useMemo(
    () =>
      teamHierarchy(data.teams, data.teamSettings)
        .ancestors.get(team.id)
        ?.some(
          (parent) =>
            parent.private ||
            data.teamSettings?.[parent.id]?.access === "private" ||
            data.teamSettings?.[parent.id]?.access === "restricted",
        ),
    [data.teams, data.teamSettings, team.id],
  );
  const hasActiveSubTeams = useMemo(() => {
    const subtree = teamHierarchy(data.teams, data.teamSettings).subtree(
      team.id,
    );
    return [...subtree].some((id) => {
      if (id === team.id) return false;
      const item = data.teams.find((candidate) => candidate.id === id);
      return Boolean(item && !item.retiredAt);
    });
  }, [data.teams, data.teamSettings, team.id]);
  const permissionLabels = {
    allMembers: "All team members",
    teamMembers: "All team members",
    owners: "Only team owners",
  };
  // "teamMembers" is a legacy alias of "allMembers"; it is shown but not offered.
  const permissionOptions = ["allMembers", "owners"];
  const audience = getIssueSharingAudience();

  const persist = async (
    patch: Partial<TeamSettings> & { applyToSubTeams?: boolean },
  ) => {
    try {
      await updateStructuredTeamSettings(team.id, patch);
      await onReload();
    } catch (error) {
      toast.error(message(error));
    }
  };

  const savePermissionFinal = async (key: TeamPermissionKey, value: string) => {
    const previous = settings[key] ?? "allMembers";
    if (hasActiveSubTeams && isLessRestrictivePermission(previous, value)) {
      const choice = await confirmApplyPermissionToSubTeams();
      if (!choice) return;
      await persist({
        [key]: value,
        ...(choice === "applyToSubTeams" ? { applyToSubTeams: true } : {}),
      } as Partial<TeamSettings> & { applyToSubTeams?: boolean });
      return;
    }
    await save({ [key]: value } as Partial<TeamSettings>);
  };

  const saveMembershipFinal = async (
    value: TeamSettings["membershipRestriction"],
  ) => {
    const previous = settings.membershipRestriction;
    if (hasActiveSubTeams && isLessRestrictiveMembership(previous, value)) {
      if (value === "open") {
        const choice = await confirmAllowSubTeamsMembership();
        if (!choice) return;
        await persist({
          membershipRestriction: value,
          ...(choice === "allowSubTeams" ? { applyToSubTeams: true } : {}),
        });
        return;
      }
      const choice = await confirmApplyPermissionToSubTeams();
      if (!choice) return;
      await persist({
        membershipRestriction: value,
        ...(choice === "applyToSubTeams" ? { applyToSubTeams: true } : {}),
      });
      return;
    }
    await save({ membershipRestriction: value });
  };

  return (
    <>
      <p className="settings-section-note team-access-note">
        {data.viewerRole === "owner"
          ? "All workspace owners and admins are automatically considered team owners"
          : "All workspace admins are automatically considered team owners"}
      </p>
      <TeamSection title="Team access">
        <SelectRow
          title="Team access"
          description="Control who can access the team and its content"
          value={settings.access}
          options={
            settings.access === "restricted"
              ? ["public", "private", "restricted"]
              : ["public", "private"]
          }
          labels={{
            public: restrictedParent
              ? "Restricted to parent team"
              : "Public to workspace",
            private: "Private",
            restricted: "Restricted to parent team",
          }}
          onChange={(value) =>
            save({ access: value as TeamSettings["access"] })
          }
        />
        <SelectRow
          title="Restrict membership"
          description={
            settings.access === "private"
              ? "Invites are always required to join private teams"
              : "Choose how members can be added to this team"
          }
          value={settings.membershipRestriction}
          options={["open", "members", "owners"]}
          labels={{
            open: "Allow anyone with access to join",
            members: "Require invite to join",
            owners: "Only team owners",
          }}
          onChange={(value) =>
            void saveMembershipFinal(
              value as TeamSettings["membershipRestriction"],
            )
          }
        />
      </TeamSection>
      <TeamSection title="Team permissions" description="Choose who can perform various actions within this team">
        {(
          [
            ["settingsPermission", "Settings management", "Who can manage the team’s settings and workflows"],
            ["labelPermission", "Label management", "Who can create, update, and delete team labels"],
            ["templatePermission", "Template management", "Who can manage team templates and recurring issues"],
            ["pinnedViewPermission", "Pinned view management", "Who can pin views to team pages and edit, reorder, or remove them"],
            ["agentSkillPermission", "Agent skills management", "Who can create, update, and delete shared skills for Flow Agent"],
            ["loopPermission", "Loop management", "Who can create, update, and delete team loops"],
            ["memberPermission", "Member management", "Who can add or remove team members — excludes guests"],
          ] as const
        ).map(([key, label, description]) => (
          <SelectRow
            key={key}
            title={label}
            description={description}
            value={settings[key] ?? "allMembers"}
            options={permissionOptions}
            labels={permissionLabels}
            onChange={(value) => void savePermissionFinal(key, value)}
          />
        ))}
      </TeamSection>
      <TeamSection
        title="Issue sharing"
        description={`Control whether issues from this team can be shared with ${audience}`}
      >
        <ToggleRow
          title="Issue sharing"
          description={`Allow issues from this team to be shared with ${audience}`}
          checked={Boolean(settings.issueSharingEnabled)}
          onChange={(value) => save({ issueSharingEnabled: value })}
        />
        {settings.issueSharingEnabled && (
          <SelectRow
            title="Who can share issues"
            description={`Control who can share issues with ${audience}`}
            value={settings.issueSharingPermission ?? "allMembers"}
            options={permissionOptions}
            labels={permissionLabels}
            onChange={(value) =>
              save({
                issueSharingPermission:
                  value as TeamSettings["issueSharingPermission"],
              })
            }
          />
        )}
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
  const navigate = useNavigate();
  const { settings, setSettings, save } = useTeamSettings(data, team, onReload);
  const slack = data.integrationConnections.find(
    (item) =>
      item.provider.toLowerCase() === "slack" && item.status === "connected",
  );
  const choices = [
    ["projectUpdates", "New project update is posted"],
    ["issueCreated", "An issue is added to the team"],
    ["issueCompleted", "An issue is marked completed or canceled"],
    ["issueStatusChanged", "An issue changes status"],
    ["commentCreated", "Comments to issues"],
    ["issueTriage", "An issue is added to the triage queue"],
  ] as const;
  return (
    <>
      <TeamSection>
        {slack ? (
          <InputRow
            title="Connect a Slack channel"
            description="Connect a channel to broadcast notifications from this team, for example #engineering"
            value={settings.slackChannelName ?? ""}
            onChange={(value) =>
              setSettings((current) => ({ ...current, slackChannelName: value }))
            }
            onCommit={(value) => save({ slackChannelName: value })}
          />
        ) : (
          <TeamRow
            title="Connect a Slack channel"
            description="Connect Slack to your workspace to broadcast notifications from this team"
          >
            <button
              type="button"
              className="settings-action"
              onClick={() => navigate(settingsPath(data.workspace.urlKey, "integrations") + "/slack")}
            >
              {t("Connect")}
            </button>
          </TeamRow>
        )}
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
              checked={
                key === "issueCompleted"
                  ? Boolean(settings.slackNotifications.issueCompleted || settings.slackNotifications.issueCanceled)
                  : settings.slackNotifications[key] ?? false
              }
              onChange={(value) =>
                save({
                  slackNotifications: {
                    ...settings.slackNotifications,
                    [key]: value,
                    ...(key === "issueCompleted" ? { issueCanceled: value } : {}),
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
  subPath,
  onNavigateSubPath,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
  subPath?: string;
  onNavigateSubPath?: (subPath?: string) => void;
}) {
  const { formatDate, t } = useI18n();
  const issues = data.issues.filter(
    (issue) => issue.team.id === team.id && issue.recurrence,
  );
  const sourceId = new URLSearchParams(window.location.search).get('fromIssue');
  const source = data.issues.find(issue => issue.id === sourceId && issue.team.id === team.id);
  const [creatingLocal, setCreatingLocal] = useState(Boolean(source));
  const creating = creatingLocal || subPath === "new";
  const setCreating = (value: boolean) => {
    setCreatingLocal(value);
    if (!source) onNavigateSubPath?.(value ? "new" : undefined);
  };
  const [title, setTitle] = useState(source?.title ?? "");
  useEffect(() => { if (source && !creating && !title) { setCreatingLocal(true); setTitle(source.title) } }, [source, creating, title]);
  const closeCreation = () => {
    setCreating(false); setTitle("");
    const url = new URL(window.location.href);
    if (url.searchParams.has('fromIssue')) { url.searchParams.delete('fromIssue'); window.history.replaceState(window.history.state, '', url) }
  };
  const [cadence, setCadence] = useState<"daily" | "weekly" | "monthly">(
    "weekly",
  );
  const create = async () => {
    if (!title.trim()) return;
    try {
      if (source) {
        await updateIssue(source.id, {title: title.trim(), recurrence: cadence});
      } else {
      const issue = await createIssue({
        title: title.trim(),
        description: "",
        teamId: team.id,
      });
      await updateIssue(issue.id, { recurrence: cadence });
      }
      closeCreation();
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
              onClick={closeCreation}
            >
              <X size={14} />
            </button>
            <button
              className="settings-action primary"
              disabled={!title.trim()}
            >
              {t(source ? "Save" : "Create")}
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
  const navigate = useNavigate();
  const gitConnected = data.integrationConnections.some(
    (item) => (item.provider === "github" || item.provider === "gitlab") && item.status === "connected",
  );
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
    if (!releaseState) return;
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
      {gitConnected ? (
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
          <div key={item.id}>
          <div className="automation-rule-row">
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
          {prRows.map(([event,label])=><SelectRow key={event} title={label} value={item.automationStates?.[event] ?? '__inherit__'} options={['__inherit__',...stateOptions]} labels={{...stateLabels,__inherit__:'Use team default'}} entityOptions={states.map(state=>state.id)} onChange={async value=>{const automationStates={...item.automationStates};if(value==='__inherit__')delete automationStates[event];else automationStates[event]=value;await upsertTargetBranch({...item,automationStates});await onReload()}}/>)}
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
        </>
      ) : (
        <TeamSection
          title="Pull request automations"
          description="With Git integrations, you can automate issue workflows when opening pull requests"
        >
          {(["github", "gitlab"] as const).map((provider) => (
            <TeamRow key={provider} title={provider === "github" ? "GitHub" : "GitLab"}>
              <button
                type="button"
                className="settings-action"
                onClick={() => navigate(`${settingsPath(data.workspace.urlKey, "integrations")}/${provider}`)}
              >
                Connect
              </button>
            </TeamRow>
          ))}
        </TeamSection>
      )}
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
              disabled={!releaseState}
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
        <TeamRow title={data.workspaceSettings.featureFlags["triage-intelligence"] ? "Triage Intelligence is enabled in this workspace" : "Triage Intelligence is not enabled in this workspace"}>
          <span className="settings-static">
            {data.workspaceSettings.featureFlags["triage-intelligence"] ? "Enabled" : "View settings"}
          </span>
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



function TeamAgentConnectorsSettings({
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
  const connectors = settings.agentConnectors ?? [];
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const add = async () => {
    if (!name.trim() || !url.trim()) return;
    await save({
      agentConnectors: [
        ...connectors,
        {
          id: `connector_${Date.now()}`,
          name: name.trim(),
          url: url.trim(),
          enabled: true,
        },
      ],
    });
    setName("");
    setUrl("");
    setCreating(false);
  };
  return (
    <TeamSection
      title={t("Agent connectors")}
      action={
        <button className="settings-action" onClick={() => setCreating(true)}>
          <Plus size={13} />
          {t("Add connector")}
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
              placeholder={t("Connector name")}
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
            <input
              className="settings-input"
              placeholder="https://mcp.example.com"
              value={url}
              onChange={(event) => setUrl(event.target.value)}
            />
            <footer>
              <button
                type="button"
                className="settings-action"
                onClick={() => setCreating(false)}
              >
                {t("Cancel")}
              </button>
              <button
                className="settings-action primary"
                disabled={!name.trim() || !url.trim()}
              >
                {t("Add")}
              </button>
            </footer>
          </form>
        )}
        {connectors.map((connector) => (
          <div className="agent-skill-row" key={connector.id}>
            <div>
              <strong data-i18n-ignore>{connector.name}</strong>
              <small data-i18n-ignore>{connector.url}</small>
            </div>
            <SettingsToggle
              checked={connector.enabled}
              label={connector.name}
              onChange={(value) => {
                void save({
                  agentConnectors: connectors.map((item) =>
                    item.id === connector.id ? { ...item, enabled: value } : item,
                  ),
                });
              }}
            />
            <button
              className="settings-action"
              aria-label={t("Remove connector")}
              onClick={() => {
                void save({
                  agentConnectors: connectors.filter(
                    (item) => item.id !== connector.id,
                  ),
                });
              }}
            >
              <Trash2 size={13} />
            </button>
          </div>
        ))}
        {!connectors.length && !creating && (
          <TeamEmpty
            icon={<Bot size={24} />}
            title={t("No team agent connectors")}
            description={t(
              "Add MCP connectors that team members can use with Flow Agent.",
            )}
          />
        )}
      </div>
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

type TemplateKind = "issue" | "project" | "document";
type AnyTemplate = IssueTemplate | ProjectTemplate | DocumentTemplate;
const NO_TEMPLATE = "none";

function TemplatesSettings({
  data,
  team,
  onReload,
  subPath,
  onNavigateSubPath,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
  subPath?: string;
  onNavigateSubPath?: (subPath?: string) => void;
}) {
  const { t } = useI18n();
  const { settings, save } = useTeamSettings(data, team, onReload);
  // The URL (templates/{kind}/new | templates/{kind}/{id}/edit) owns the
  // open editor so it survives reloads and can be linked to.
  const [routeKind, routeId, routeAction] = (subPath ?? "").split("/");
  const editingKind = (["issue", "project", "document"] as const).find((kind) => kind === routeKind);
  const scopeIds = new Set([
    team.id,
    ...(teamHierarchy(data.teams, data.teamSettings).ancestors.get(team.id) ?? []).map((item) => item.id),
  ]);
  const templatesByKind: Record<TemplateKind, AnyTemplate[]> = {
    issue: data.issueTemplates.filter((item) => scopeIds.has(item.teamId ?? "")),
    project: data.projectTemplates.filter(
      (item) => item.visibility === "teams" && item.teamIds.some((teamId) => scopeIds.has(teamId)),
    ),
    document: data.documentTemplates.filter((item) => scopeIds.has(item.teamId ?? "")),
  };
  const editing: AnyTemplate | null | undefined = !editingKind
    ? undefined
    : routeId === "new"
      ? null
      : routeAction === "edit"
        ? templatesByKind[editingKind].find((item) => item.id === routeId)
        : undefined;
  const openEditor = (kind: TemplateKind, template: AnyTemplate | null) =>
    onNavigateSubPath?.(template === null ? `${kind}/new` : `${kind}/${template.id}/edit`);
  const closeEditor = () => onNavigateSubPath?.(undefined);
  const sourceTeamOf = (kind: TemplateKind, template: AnyTemplate) =>
    kind === "project"
      ? (template as ProjectTemplate).teamIds.find((teamId) => scopeIds.has(teamId) && teamId !== team.id)
      : (template as IssueTemplate | DocumentTemplate).teamId && (template as IssueTemplate | DocumentTemplate).teamId !== team.id
        ? (template as IssueTemplate | DocumentTemplate).teamId
        : undefined;
  const templateSelect = (
    title: string,
    value: string | undefined,
    options: AnyTemplate[],
    onChange: (id: string) => void,
  ) => (
    <SelectRow
      title={title}
      value={value || NO_TEMPLATE}
      options={[NO_TEMPLATE, ...options.map((item) => item.id)]}
      labels={{ [NO_TEMPLATE]: "No template", ...Object.fromEntries(options.map((item) => [item.id, item.name])) }}
      entityOptions={options.map((item) => item.id)}
      onChange={(next) => onChange(next === NO_TEMPLATE ? "" : next)}
    />
  );
  const section = (kind: TemplateKind) => {
    const templates = templatesByKind[kind];
    return (
      <TeamSection key={kind} title={`${titleCase(kind)} templates`}>
        {templates.map((template) => {
          const sourceTeamID = sourceTeamOf(kind, template);
          const inherited = Boolean(sourceTeamID);
          return (
            <TeamRow
              key={template.id}
              className={inherited ? "team-template-row is-inherited" : "personal-row-link team-template-row"}
              title={<span data-i18n-ignore>{template.name}</span>}
              description={
                inherited ? (
                  <>
                    {t("Inherited from")}{" "}
                    <span data-i18n-ignore>{data.teams.find((item) => item.id === sourceTeamID)?.name ?? t("parent team")}</span>
                  </>
                ) : template.description ? (
                  <span data-i18n-ignore>{template.description}</span>
                ) : undefined
              }
              role={inherited ? undefined : "button"}
              tabIndex={inherited ? undefined : 0}
              onClick={inherited ? undefined : () => openEditor(kind, template)}
              onKeyDown={inherited ? undefined : (event) => { if (event.key === "Enter") openEditor(kind, template); }}
            />
          );
        })}
        <TeamRow className="settings-row--action" title={templates.length ? "" : `No ${kind} templates`}>
          <button className="settings-action" type="button" onClick={() => openEditor(kind, null)}>
            <Plus size={13} />
            {t("New template")}
          </button>
        </TeamRow>
      </TeamSection>
    );
  };
  const issueTemplates = templatesByKind.issue as IssueTemplate[];
  return (
    <>
      {section("issue")}
      {issueTemplates.length > 0 && (
        <TeamSection
          title="Default issue template"
          description="Pre-select a template when creating an issue for this team. Form templates can’t be used as a default for team members."
        >
          {templateSelect(
            "Issues created by team members",
            settings.defaultIssueTemplateForMembersId,
            issueTemplates.filter((item) => item.templateType !== "customForm"),
            (id) => void save({ defaultIssueTemplateForMembersId: id }),
          )}
          {templateSelect(
            "Issues created by non-team members",
            settings.defaultIssueTemplateForNonMembersId,
            issueTemplates,
            (id) => void save({ defaultIssueTemplateForNonMembersId: id }),
          )}
        </TeamSection>
      )}
      {section("project")}
      {templatesByKind.project.length > 0 && (
        <TeamSection title="Default project template" description="Pre-select a template when creating a project for this team">
          {templateSelect(
            "Projects created",
            settings.defaultProjectTemplateId,
            templatesByKind.project,
            (id) => void save({ defaultProjectTemplateId: id }),
          )}
        </TeamSection>
      )}
      {section("document")}
      {editing !== undefined && editingKind &&
        (editingKind === "document" ? (
          <DocumentTemplateEditor
            team={team}
            template={editing as DocumentTemplate | null}
            onClose={closeEditor}
            onSaved={onReload}
          />
        ) : (
          <TemplateEditor
            data={data}
            type={editingKind}
            teamId={team.id}
            template={editing as IssueTemplate | ProjectTemplate | null}
            onClose={closeEditor}
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
  const teamSettings = data.teamSettings?.[team.id];
  const inherited = Boolean(teamSettings?.inheritWorkflowStatuses && teamSettings.parentTeamId);
  const [states, setStates] = useState<WorkflowState[]>(
    statesForTeam(data, team.id),
  );
  const [createType, setCreateType] = useState<WorkflowStateType | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [dragging, setDragging] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<WorkflowState | null>(null);
  const [counts, setCounts] = useState<Record<string,number>>({});
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string>();
  const [loadRetry, setLoadRetry] = useState(0);
  useEffect(() => {
    let active=true;
    const controller=new AbortController();
    setLoading(true);
    setLoadError(undefined);
    if (inherited) {
      setStates(statesForTeam(data, team.id));
      setCounts({});
      setLoading(false);
      return () => controller.abort();
    }
    void Promise.all([fetchWorkflowStates(team.id),listIssueRecordGroups({teamId:team.id,includeSubTeams:false,groupBy:'status',archived:'all'},controller.signal)])
      .then(([next,usage])=>{if(active){setStates(next);setCounts(Object.fromEntries(usage.groups.map(group=>[group.value,group.count])))}})
      .catch(error=>{if(active)setLoadError(message(error))})
      .finally(()=>{if(active)setLoading(false)});
    return ()=>{active=false;controller.abort()};
  }, [data, inherited, team.id, loadRetry]);
  const reload = async () => {
    if (inherited) {
      setStates(statesForTeam(data, team.id));
      setCounts({});
      await onReload();
      return;
    }
    const [next,usage]=await Promise.all([fetchWorkflowStates(team.id),listIssueRecordGroups({teamId:team.id,includeSubTeams:false,groupBy:'status',archived:'all'})]);
    setStates(next);
    setCounts(Object.fromEntries(usage.groups.map(group=>[group.value,group.count])));
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
      <TeamSettingsCrumb team={team} onClick={onBack} />
      <header className="settings-page-header ip-page-header team-statuses-header">
        <div>
          <h1>{t("Issue statuses")}</h1>
          <p>
            {t(
              "Issue statuses define the workflow that issues go through from start to completion.",
            )}
          </p>
        </div>
      </header>
      {inherited && <div className="team-inherited-setting"><StatusIcon state={states[0]}/><div><strong>{t("Issue statuses are inherited")}</strong><p>{t("Manage statuses from the parent team. Changes are synced automatically.")}</p></div></div>}
      {loading&&<p role="status">{t('Loading issue statuses…')}</p>}
      {loadError&&<div className="workflow-status-load-error" role="alert"><span>{t('Could not load issue statuses')}: {t(loadError)}</span><button type="button" onClick={()=>setLoadRetry(value=>value+1)}>{t('Try again')}</button></div>}
      {!loading&&!loadError&&!inherited&&<div className="workflow-default-state"><PropertyMenu label={t('Default status')} ariaLabel={t('Default status')} searchPlaceholder={t('Search statuses…')} icon={states.some(state=>state.default)?<StatusIcon state={states.find(state=>state.default)!}/>:undefined} valueIsEntityName value={states.find(state=>state.default)?.name} selectedId={states.find(state=>state.default)?.id} options={states.filter(state=>!state.reserved).map(state=>({id:state.id,label:state.name,i18nIgnore:true,icon:<StatusIcon state={state}/>}))} onChange={id=>run(()=>updateWorkflowState(team.id,id,{default:true}))}/></div>}
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
            canModify = !group.reserved && !inherited;
          return (
            <div className="ip-status-section" role="list" key={group.label}>
              <header>
                <h3>{t(group.label)}</h3>
                {!group.reserved && (
                  <button
                    aria-label={t("Create new workflow state")}
                    disabled={busy || inherited}
                    onClick={() => setCreateType(group.type)}
                  >
                    <Plus />
                  </button>
                )}
              </header>
              {groupStates.map((state) => {
                const usage = loading||loadError ? undefined : counts[state.id]??0;
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
                    canDelete={canModify&&groupStates.length>1}
                    canReorder={canModify&&groupStates.length>1}
                    dragging={dragging === state.id}
                    workspaceKey={data.workspace.urlKey}
                    teamKey={team.key}
                    onDragStart={() => setDragging(state.id)}
                    onDragEnd={() => setDragging(null)}
                    onDrop={() => drop(state)}
                    onMove={(delta) => move(state, delta)}
                    onEdit={() => setEditing(state.id)}
                    onDelete={() => {
                      if(canModify&&groupStates.length>1)setDeleting(state);
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
      {deleting&&<WorkflowStateDeleteDialog key={deleting.id} teamId={team.id} state={states.find(state=>state.id===deleting.id)??deleting} states={states} usage={loading||loadError?undefined:counts[deleting.id]??0} loading={loading} loadError={loadError} onRetry={()=>setLoadRetry(value=>value+1)} onClose={()=>setDeleting(null)} onDeleted={async()=>{setStates(current=>current.filter(state=>state.id!==deleting.id));setDeleting(null);try{await reload()}catch(error){setLoadError(message(error))}}} onDefaultChanged={reload}/>}
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
  canDelete,
  canReorder,
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
  usage?: number;
  canModify: boolean;
  canDelete: boolean;
  canReorder: boolean;
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
  const viewHref = `/${encodeURIComponent(workspaceKey)}/team/${encodeURIComponent(teamKey)}/all?status=${encodeURIComponent(state.id)}`;
  const menuKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const items = [
        ...event.currentTarget.querySelectorAll<HTMLButtonElement>(
          "[role=option]:not(:disabled)",
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
    usage !== undefined && usage > 0 ? (
      canModify ? (
        <AppLink
          className="ip-status-usage"
          aria-label={t("View issues")}
          href={viewHref}
          onClick={(event) => {
            event.stopPropagation();
          }}
        >
          {usage} {t(usage === 1 ? "issue" : "issues")}
        </AppLink>
      ) : (
        <span className="ip-status-usage is-disabled">
          {usage} {t(usage === 1 ? "issue" : "issues")}
        </span>
      )
    ) : null;
  return (
    <div
      className={`ip-status-row issue-state-row${dragging ? " is-dragging" : ""}`}
      role="button"
      aria-disabled={!canModify}
      aria-roledescription={canReorder?"sortable":undefined}
      tabIndex={canReorder ? 0 : -1}
      draggable={canReorder}
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onDragOver={(event) => {
        if (canReorder) event.preventDefault();
      }}
      onDrop={(event) => {
        event.preventDefault();
        onDrop();
      }}
      onKeyDown={(event) => {
        if (!canReorder || !event.altKey) return;
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
      {canReorder && <StatusDragHandle />}
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
            <Popover.Content data-flow-motion="floating"
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
                disabled={!canDelete}
                title={!canDelete?t("You can't delete the last status of a type."):undefined}
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
  const { settings: teamSettings, save: saveTeamSettings } = useTeamSettings(
    data,
    team,
    onReload,
  );
  const initial = data.cycleSettings[team.id] ?? defaultCycles();
  const inherited = Boolean(teamSettings.inheritCycles && teamSettings.parentTeamId);
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
      {teamSettings.parentTeamId && (
        <TeamSection title="Team hierarchy">
          <ToggleRow
            title="Inherit cycles from parent team"
            description="Keep this team's cycle schedule in sync with its parent team."
            checked={teamSettings.inheritCycles}
            onChange={(value) => void saveTeamSettings({ inheritCycles: value })}
          />
        </TeamSection>
      )}
      {inherited ? (
        <TeamSection>
          <TeamRow title="Cycles inherited from parent team" description="Manage the schedule and cycle settings from the parent team.">
            <span className="team-inherited-value">{data.teams.find((item) => item.id === teamSettings.parentTeamId)?.name}</span>
          </TeamRow>
        </TeamSection>
      ) : <>
      <TeamSection>
        <ToggleRow
          title="Enable cycles"
          checked={settings.enabled}
          onChange={(value) => save({ enabled: value })}
        />
        {settings.enabled && (
          <>
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
              title="Cooldown duration"
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
            <SelectRow
              title="Cycle start"
              description="Future cycles begin on the same day of the week"
              value={String(settings.startsOn)}
              options={["1", "2", "3", "4", "5", "6", "0"]}
              labels={{ "0": "Sunday", "1": "Monday", "2": "Tuesday", "3": "Wednesday", "4": "Thursday", "5": "Friday", "6": "Saturday" }}
              onChange={(value) => save({ startsOn: Number(value) })}
            />
            <SelectRow
              title="Auto-create cycles"
              description="Number of upcoming cycles created in advance"
              value={settings.autoCreate ? String(settings.upcomingCount) : "0"}
              options={["0", "1", "2", "3", "4", "6"]}
              labels={Object.fromEntries(
                ["0", "1", "2", "3", "4", "6"].map((value) => [
                  value,
                  value === "0" ? "Off" : `${value} upcoming cycle${value === "1" ? "" : "s"}`,
                ]),
              )}
              onChange={(value) =>
                save(
                  value === "0"
                    ? { autoCreate: false }
                    : { autoCreate: true, upcomingCount: Number(value) },
                )
              }
            />
            <NumberRow
              title="Capacity"
              description="Planning capacity shown on each generated cycle."
              value={settings.capacity}
              onCommit={(value) => save({ capacity: value })}
            />
          </>
        )}
      </TeamSection>
      <TeamSection
        title="Cycle automation"
        description="Capture all work in cycles by auto-adding issues to cycles based on their status type"
      >
        <ToggleRow
          title="Active issues & due date"
          description="Auto-add started, unstarted, and issues with due dates that match the current cycle, or the next if in cooldown"
          checked={settings.autoAddActive && settings.autoAddDueDate}
          onChange={(value) => save({ autoAddActive: value, autoAddDueDate: value })}
        />
        {!(settings.autoAddActive && settings.autoAddDueDate) && (
          <ToggleRow
            title="Started issues"
            description="Auto-add started issues to the current cycle, or the next if in cooldown"
            checked={settings.autoAddStarted}
            onChange={(value) => save({ autoAddStarted: value })}
          />
        )}
        <ToggleRow
          title="Completed issues"
          description="Auto-add completed issues to the current cycle, or the next if in cooldown"
          checked={settings.autoAddCompleted}
          onChange={(value) => save({ autoAddCompleted: value })}
        />
        <ToggleRow
          title="Unfinished issues"
          description="Move unfinished issues to the next cycle when a cycle ends"
          checked={settings.autoMigrate}
          onChange={(value) => save({ autoMigrate: value })}
        />
      </TeamSection>
      {!settings.enabled && data.cycles.some((cycle) => cycle.teamId === team.id) && (
        <TeamSection title="Danger zone">
          <TeamRow
            danger
            title="Delete historical cycle data"
            description="Permanently delete this team’s past cycles. Issues keep their other data."
          >
            <button
              type="button"
              className="settings-action danger"
              onClick={() =>
                void (async () => {
                  if (
                    !(await confirmAction("Permanently delete cycles data?", {
                      confirmLabel: "Delete",
                      description: "All of this team’s cycles will be deleted. This cannot be undone.",
                    }))
                  )
                    return;
                  try {
                    await deleteTeamCycles(team.id);
                    await onReload();
                  } catch (error) {
                    toast.error(message(error));
                  }
                })()
              }
            >
              Delete…
            </button>
          </TeamRow>
        </TeamSection>
      )}
      </>}
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
  icons = {},
  entityOptions = [],
  onChange,
}: {
  title: string;
  description?: string;
  value: string;
  options: string[];
  labels?: Record<string, string>;
  icons?: Record<string, ReactNode>;
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
            icon: icons[option],
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
function statesForTeam(data: BootstrapData, teamId: string, seen = new Set<string>()): WorkflowState[] {
  if (seen.has(teamId)) return [];
  seen.add(teamId);
  const settings = data.teamSettings?.[teamId];
  if (settings?.inheritWorkflowStatuses && settings.parentTeamId) {
    return statesForTeam(data, settings.parentTeamId, seen);
  }
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
    issueSharingEnabled: false,
    issueSharingPermission: "allMembers",
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
    inheritIssueEstimation: false,
    inheritWorkflowStatuses: false,
    inheritProjectStatuses: false,
    inheritCycles: false,
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
function message(error: unknown) {
  return error instanceof Error
    ? error.message
    : "Could not save team settings";
}
function titleCase(value: string) {
  return value.slice(0, 1).toUpperCase() + value.slice(1);
}
import { AppLink } from '@/components/ui/app-link';
