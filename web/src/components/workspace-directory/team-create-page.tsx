import {
  Activity,
  AppWindow,
  ArrowLeft,
  Bell,
  Bot,
  Braces,
  Building2,
  CircleDot,
  Code2,
  FileText,
  Flame,
  Goal,
  Import,
  KeyRound,
  LayoutTemplate,
  Link2,
  MessageCircleQuestion,
  MousePointer2,
  PanelTop,
  Plug,
  Radio,
  Search,
  ShieldCheck,
  SlidersHorizontal,
  Smile,
  Sparkles,
  Tag,
  UserRound,
  UsersRound,
  type LucideIcon,
} from "lucide-react";
import { useMemo, useState, type FormEvent } from "react";

import { ViewIconPicker } from "@/components/views/view-icon-picker";
import { TeamIcon } from "@/components/issue/issue-icons";
import { SelectControl } from "@/components/ui/select-control";
import { ReleasesIcon } from "@/components/releases/release-icons";
import type { Team } from "@/types/flow";
import type { SettingsPageId } from "@/lib/app-routes";

import "./workspace-directory.css";

const SETTINGS_SECTIONS: {
  title: string;
  items: { label: string; icon: LucideIcon }[];
}[] = [
  {
    title: "Personal",
    items: [
      { label: "Preferences", icon: SlidersHorizontal },
      { label: "Profile", icon: UserRound },
      { label: "Notifications", icon: Bell },
      { label: "Code & reviews", icon: Code2 },
      { label: "Security & access", icon: KeyRound },
      { label: "Connected accounts", icon: Link2 },
      { label: "Agent personalization", icon: MousePointer2 },
    ],
  },
  {
    title: "Issues",
    items: [
      { label: "Labels", icon: Tag },
      { label: "Templates", icon: LayoutTemplate },
      { label: "SLAs", icon: Flame },
    ],
  },
  {
    title: "Projects",
    items: [
      { label: "Labels", icon: Tag },
      { label: "Templates", icon: PanelTop },
      { label: "Statuses", icon: CircleDot },
      { label: "Updates", icon: Activity },
    ],
  },
  {
    title: "Features",
    items: [
      { label: "AI & Agents", icon: Sparkles },
      { label: "Initiatives", icon: Goal },
      { label: "Documents", icon: FileText },
      { label: "Customer requests", icon: UsersRound },
      { label: "Releases", icon: ReleasesIcon },
      { label: "Pulse", icon: Radio },
      { label: "Asks", icon: MessageCircleQuestion },
      { label: "Emojis", icon: Smile },
      { label: "Integrations", icon: Plug },
    ],
  },
  {
    title: "Administration",
    items: [
      { label: "Workspace", icon: Building2 },
      { label: "Teams", icon: UsersRound },
      { label: "Members", icon: UserRound },
      { label: "Security", icon: ShieldCheck },
      { label: "API", icon: Braces },
      { label: "Applications", icon: AppWindow },
      { label: "Import & export", icon: Import },
    ],
  },
];

const SETTINGS_DESTINATIONS: Record<string, SettingsPageId> = {
  "Personal/Preferences":"preferences","Personal/Profile":"profile","Personal/Notifications":"notifications","Personal/Code & reviews":"code-and-reviews","Personal/Security & access":"account-security","Personal/Connected accounts":"connections","Personal/Agent personalization":"agents",
  "Issues/Labels":"issue-labels","Issues/Templates":"issue-templates","Issues/SLAs":"sla",
  "Projects/Labels":"project-labels","Projects/Templates":"project-templates","Projects/Statuses":"project-statuses","Projects/Updates":"project-updates",
  "Features/AI & Agents":"ai","Features/Initiatives":"initiatives","Features/Documents":"documents","Features/Customer requests":"customer-requests","Features/Releases":"releases","Features/Pulse":"pulse","Features/Asks":"asks","Features/Emojis":"emojis","Features/Integrations":"integrations",
  "Administration/Workspace":"workspace","Administration/Teams":"teams","Administration/Members":"members","Administration/Security":"security","Administration/API":"api","Administration/Applications":"applications","Administration/Import & export":"import-export",
};

export function TeamCreatePage({
  teams,
  onBack,
  onNavigateSettings,
  onCreate,
}: {
  teams: Team[];
  onBack: () => void;
  onNavigateSettings: (page: SettingsPageId, teamKey?: string) => void;
  onCreate: (input: {
    name: string;
    key: string;
    color?: string;
    icon?: string;
    private?: boolean;
    parentTeamId?: string;
    copyFromTeamId?: string;
    timezone?: string;
  }) => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const [keyEdited, setKeyEdited] = useState(false);
  const [color, setColor] = useState("#bec2c8");
  const [icon, setIcon] = useState("Team");
  const [settingsQuery, setSettingsQuery] = useState("");
  const [timezone, setTimezone] = useState(
    "GMT+8:00 – China Standard Time - Shanghai",
  );
  const [copyFrom, setCopyFrom] = useState("");
  const [parentTeamId, setParentTeamId] = useState("");
  const [privateTeam, setPrivateTeam] = useState(false);
  const [saving, setSaving] = useState(false);
  const generatedKey = useMemo(() => teamCode(name), [name]);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const identifier = (keyEdited ? key : generatedKey).trim();
    if (!name.trim() || !identifier || saving) return;
    setSaving(true);
    try {
      await onCreate({ name: name.trim(), key: identifier, color, icon, private: privateTeam, parentTeamId, copyFromTeamId: parentTeamId || copyFrom, timezone: timezone.includes("Coordinated") ? "Etc/UTC" : "Asia/Shanghai" });
      onBack();
    } finally {
      setSaving(false);
    }
  };
  const query = settingsQuery.trim().toLowerCase();
  return (
    <div className="workspace-new-team">
      <aside className="workspace-settings-nav">
        <button type="button" onClick={onBack}>
          <ArrowLeft />
          Back to app
        </button>
        <label>
          <Search />
          <input
            aria-label="Search settings"
            placeholder="Search…"
            value={settingsQuery}
            onChange={(event) => setSettingsQuery(event.target.value)}
          />
        </label>
        {SETTINGS_SECTIONS.map((section) => {
          const items = section.items.filter(
            (item) => !query || item.label.toLowerCase().includes(query),
          );
          return items.length ? (
            <div className="workspace-settings-nav__group" key={section.title}>
              <h2>{section.title}</h2>
              {items.map((item) => (
                <button
                  className={item.label === "Teams" ? "active" : ""}
                  key={`${section.title}-${item.label}`}
                  onClick={() => onNavigateSettings(SETTINGS_DESTINATIONS[`${section.title}/${item.label}`] ?? "workspace")}
                  type="button"
                >
                  <item.icon />
                  {item.label}
                </button>
              ))}
            </div>
          ) : null;
        })}
        {!query && (
          <div className="workspace-settings-nav__group">
            <h2>Your teams</h2>
            {teams.map((team) => (
              <button key={team.id} onClick={()=>onNavigateSettings("team",team.key)} type="button">
                <Bot />
                {team.name}
              </button>
            ))}
          </div>
        )}
      </aside>
      <main className="workspace-new-team__main">
        <button
          className="workspace-new-team__back"
          type="button"
          onClick={onBack}
        >
          <ArrowLeft />
          Back
        </button>
        <form onSubmit={submit}>
          <h1>Create a new team</h1>
          <p>
            Create a new team to manage separate cycles, workflows, and
            notifications
          </p>
          <section className="workspace-settings-card">
            <label>
              <span>Icon &amp; Name</span>
              <div>
                  <ViewIconPicker
                    align="end"
                    color={color}
                    icon={icon}
                  onChange={(visual) => {
                    setColor(visual.color);
                    setIcon(visual.icon);
                    }}
                    prependTeam
                    triggerClassName="workspace-team-color"
                />
                <input
                  autoFocus
                  aria-label="Icon & Name"
                  placeholder="e.g. Engineering"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                />
              </div>
            </label>
            <label>
              <span>
                Identifier
                <small>
                  Used to identify issues from this team (e.g. ENG-123)
                </small>
              </span>
              <input
                aria-label="Identifier"
                maxLength={5}
                placeholder="e.g. ENG"
                value={keyEdited ? key : generatedKey}
                onChange={(event) => {
                  setKeyEdited(true);
                  setKey(teamCode(event.target.value));
                }}
              />
            </label>
            <label>
              <span>Parent team<small>Settings and workflows will be inherited from the parent team</small></span>
              <SelectControl label="Parent team" value={parentTeamId} onChange={(value) => { setParentTeamId(value); if (value) setCopyFrom(value); }} options={[{ value: "", label: "No parent team" }, ...teams.map(team => ({ value: team.id, label: team.name, entityName: true, icon: <TeamIcon team={team} size={14}/> }))]}/>
            </label>
          </section>
          <h2>Team access</h2>
          <p>
            Control who can access the team and its content. Private teams are
            visible only to team members and workspace admins.
          </p>
          <section className="workspace-settings-card">
            <label>
              <span>Team access</span>
              <SelectControl label="Team access" value={privateTeam ? "private" : "public"} onChange={(value) => setPrivateTeam(value === "private")} options={[{ value: "public", label: "Public to workspace" }, { value: "private", label: "Private" }]}/>
            </label>
          </section>
          <h2>Timezone</h2>
          <p>Used for team schedules, dates, and cycle start times</p>
          <section className="workspace-settings-card">
            <label>
              <span>Timezone</span>
              <SelectControl label="Timezone" value={timezone} onChange={setTimezone} options={[{ value: "GMT+8:00 – China Standard Time - Shanghai", label: "GMT+8:00 – China Standard Time - Shanghai" }, { value: "GMT+0:00 – Coordinated Universal Time", label: "GMT+0:00 – Coordinated Universal Time" }]}/>
            </label>
          </section>
          <h2>Copy settings from existing team</h2>
          <p>
            Copy workflows, cycle, and team settings from another team. Team
            members and Slack notification settings won't be copied.
          </p>
          <section className="workspace-settings-card">
            <label>
              <span>Copy from team</span>
              <SelectControl label="Copy from team" disabled={Boolean(parentTeamId)} value={copyFrom} onChange={setCopyFrom} options={[{ value: "", label: "Don’t copy" }, ...teams.map(team => ({ value: team.id, label: team.name, entityName: true, icon: <TeamIcon team={team} size={14}/> }))]}/>
            </label>
          </section>
          <button
            className="workspace-new-team__submit"
            type="submit"
            disabled={
              !name.trim() || !(keyEdited ? key : generatedKey) || saving
            }
          >
            {saving ? "Creating…" : "Create team"}
          </button>
        </form>
      </main>
    </div>
  );
}

function teamCode(value: string) {
  return value
    .toUpperCase()
    .replace(/[^A-Z0-9]/g, "")
    .slice(0, 3);
}
