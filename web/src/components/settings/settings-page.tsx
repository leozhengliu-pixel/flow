import { useEffect, useMemo, useState, type ReactNode } from "react";
import {
  Activity, AppWindow, ArrowLeft, Bell, Bot, Braces, Building2, Check, ChevronDown,
  CircleDot, Code2, CreditCard, FileText, Flame, Gauge,
  Import, KeyRound, LayoutTemplate, Link2, ListFilter, MessageCircleQuestion,
  MoreHorizontal, PanelTop, Plug, Plus, Radio, Rocket, Search, ShieldCheck,
  SlidersHorizontal, Smile, Sparkles, Tag, Trash2, Upload, UserRound, UsersRound,
  X, Zap, type LucideIcon,
} from "lucide-react";
import { toast } from "sonner";

import {
  DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { inviteMembers, removeMember, resendInvitation, revokeInvitation, setTeamMembership, suspendMember, updateMemberRole } from "@/lib/api";
import type { SettingsPageId, TeamSettingsSection } from "@/lib/app-routes";
import type { BootstrapData, Team, WorkspaceMutationInput } from "@/types/flow";
import { NotificationSettings } from "./notification-settings";
import { TeamWorkflowSettings } from "./team-workflow-settings";
import { FeatureSettings, ImportExportSettings, ProjectUpdateSettings, SLASettings, TemplateSettings } from "./advanced-settings";
import { useI18n } from "@/i18n/i18n";

import "./settings.css";
import "./workflow-settings.css";
import "./advanced-settings.css";

type StoredSettings = {
  values: Record<string, string | boolean>;
  lists: Record<string, SettingListItem[]>;
};
type SettingListItem = { id: string; name: string; description?: string; color?: string };

type SettingsPageProps = {
  data: BootstrapData;
  page: SettingsPageId;
  teamKey?: string;
  teamSection?: TeamSettingsSection;
  onBack: () => void;
  onNavigate: (page: SettingsPageId, teamKey?: string, teamSection?: TeamSettingsSection) => void;
  onCreateTeam: () => void;
  onWorkspaceUpdate: (input: WorkspaceMutationInput) => Promise<void>;
  onWorkspaceDelete: () => Promise<void>;
  onSettingsUpdate: (settings: Record<string, unknown>) => Promise<Record<string, unknown>>;
  onReload: () => Promise<void>;
};

type NavItem = { id: SettingsPageId; label: string; icon: LucideIcon };
const NAV: { title: string; items: NavItem[] }[] = [
  { title: "Personal", items: [
    { id: "preferences", label: "Preferences", icon: SlidersHorizontal },
    { id: "profile", label: "Profile", icon: UserRound },
    { id: "notifications", label: "Notifications", icon: Bell },
    { id: "code-and-reviews", label: "Code & reviews", icon: Code2 },
    { id: "account-security", label: "Security & access", icon: KeyRound },
    { id: "connections", label: "Connected accounts", icon: Link2 },
    { id: "agents", label: "Agent personalization", icon: Bot },
  ]},
  { title: "Issues", items: [
    { id: "issue-labels", label: "Labels", icon: Tag },
    { id: "issue-templates", label: "Templates", icon: LayoutTemplate },
    { id: "sla", label: "SLAs", icon: Flame },
  ]},
  { title: "Projects", items: [
    { id: "project-labels", label: "Labels", icon: Tag },
    { id: "project-templates", label: "Templates", icon: PanelTop },
    { id: "project-statuses", label: "Statuses", icon: CircleDot },
    { id: "project-updates", label: "Updates", icon: Activity },
  ]},
  { title: "Features", items: [
    { id: "ai", label: "AI & Agents", icon: Sparkles },
    { id: "initiatives", label: "Initiatives", icon: Zap },
    { id: "documents", label: "Documents", icon: FileText },
    { id: "customer-requests", label: "Customer requests", icon: UsersRound },
    { id: "releases", label: "Releases", icon: Rocket },
    { id: "pulse", label: "Pulse", icon: Radio },
    { id: "asks", label: "Asks", icon: MessageCircleQuestion },
    { id: "emojis", label: "Emojis", icon: Smile },
    { id: "integrations", label: "Integrations", icon: Plug },
  ]},
  { title: "Administration", items: [
    { id: "workspace", label: "Workspace", icon: Building2 },
    { id: "teams", label: "Teams", icon: UsersRound },
    { id: "members", label: "Members", icon: UserRound },
    { id: "security", label: "Security", icon: ShieldCheck },
    { id: "api", label: "API", icon: Braces },
    { id: "applications", label: "Applications", icon: AppWindow },
    { id: "billing", label: "Billing", icon: CreditCard },
    { id: "usage", label: "Usage & limits", icon: Gauge },
    { id: "import-export", label: "Import & export", icon: Import },
  ]},
];

const DEFAULT_VALUES: StoredSettings["values"] = {
  homeView: "Flow Agent (default)", displayNames: "Full name", firstDay: "Monday",
  emoticons: true, sendComments: "Enter", fontSize: "Default", pointerCursor: false,
  underlineLinks: false, interfaceTheme: "System preference", lightTheme: "Light",
  darkTheme: "Dark", desktopLinks: false, autoAssign: false, assignStarted: false,
  notificationEmail: true, notificationDesktop: true, notificationSound: true,
  notificationDigest: "Daily", reviewAutoAssign: true, branchFormat: "{identifier}-{title}",
  passkeys: false, agentEnabled: true, agentInstructions: "",
};

export function SettingsPage(props: SettingsPageProps) {
  const [query, setQuery] = useState("");
  const [mobileNav, setMobileNav] = useState(false);
  const [settings, setSettings] = useStoredSettings(props.data.workspace.urlKey, props.data.settings, props.data.viewerRole === "admin" ? props.onSettingsUpdate : async value => value);
  const isAdmin = props.data.viewerRole === "admin";
  const visible = useMemo(() => NAV.filter(section => isAdmin || section.title === "Personal").map(section => ({
    ...section,
    items: section.items.filter(item => item.label.toLowerCase().includes(query.toLowerCase())),
  })).filter(section => section.items.length), [isAdmin, query]);
  const setValue = (key: string, value: string | boolean) =>
    setSettings(current => ({ ...current, values: { ...current.values, [key]: value } }));
  return <div className="settings-app">
    <aside className={`settings-sidebar${mobileNav ? " open" : ""}`}>
      <button className="settings-back" onClick={props.onBack}><ArrowLeft size={16}/>Back to app</button>
      <label className="settings-search"><Search size={15}/><input aria-label="Search settings" placeholder="Search…" value={query} onChange={event => setQuery(event.target.value)}/>{query && <button aria-label="Clear search" onClick={() => setQuery("")}><X size={13}/></button>}</label>
      <nav aria-label="Settings navigation">
        {visible.map(section => <section key={section.title}><h2>{section.title}</h2>{section.items.map(item => <SettingsNavButton key={item.id} item={item} active={props.page === item.id} onClick={() => { props.onNavigate(item.id); setMobileNav(false); }}/>)}</section>)}
        {!query && props.data.viewerRole !== "guest" && <section><h2>Your teams</h2>{props.data.teams.filter(team => isAdmin || props.data.teamMembers.some(member => member.teamId === team.id && member.userId === props.data.viewer.id && member.role === "owner")).map(team => <button key={team.id} className={props.page === "team" && props.teamKey?.toLowerCase() === team.key.toLowerCase() ? "active" : ""} onClick={() => props.onNavigate("team", team.key)}><span className="settings-team-icon" style={{ color: team.color }}>{team.icon || team.key.slice(0, 1)}</span><span>{team.name}</span></button>)}{isAdmin && <button onClick={props.onCreateTeam}><Plus size={16}/><span>Create a team</span></button>}</section>}
      </nav>
    </aside>
    <button className={`settings-sidebar-scrim${mobileNav ? " open" : ""}`} aria-label="Close settings navigation" onClick={() => setMobileNav(false)}/>
    <main className="settings-main">
      <button className="settings-mobile-menu" onClick={() => setMobileNav(true)}><ListFilter size={15}/></button>
      <div className="settings-content">
        <SettingsBody {...props} settings={settings} setSettings={setSettings} setValue={setValue}/>
      </div>
    </main>
  </div>;
}

function SettingsNavButton({ item, active, onClick }: { item: NavItem; active: boolean; onClick: () => void }) {
  const Icon = item.icon;
  return <button className={active ? "active" : ""} onClick={onClick}><Icon size={16}/><span>{item.label}</span></button>;
}

function SettingsBody(props: SettingsPageProps & { settings: StoredSettings; setSettings: React.Dispatch<React.SetStateAction<StoredSettings>>; setValue: (key: string, value: string | boolean) => void }) {
  const { page } = props;
  const personal = ["preferences","profile","notifications","code-and-reviews","account-security","connections","agents"].includes(page);
  const teamOwner = page === "team" && props.data.teams.some(team => team.key.toLowerCase() === props.teamKey?.toLowerCase() && props.data.teamMembers.some(member => member.teamId === team.id && member.userId === props.data.viewer.id && member.role === "owner"));
  if (!personal && props.data.viewerRole !== "admin" && !teamOwner) return <div className="settings-empty"><ShieldCheck size={28}/><h3>Admin access required</h3><p>You don't have permission to manage this workspace setting.</p></div>;
  if (page === "preferences") return <Preferences values={props.settings.values} setValue={props.setValue}/>;
  if (page === "profile") return <ProfilePage data={props.data} values={props.settings.values} setValue={props.setValue}/>;
  if (page === "notifications") return <NotificationSettings data={props.data} onReload={props.onReload}/>;
  if (page === "code-and-reviews") return <CodeReviews values={props.settings.values} setValue={props.setValue}/>;
  if (page === "account-security") return <AccountSecurity values={props.settings.values} setValue={props.setValue}/>;
  if (page === "connections") return <Connections values={props.settings.values} setValue={props.setValue}/>;
  if (page === "agents") return <Agents values={props.settings.values} setValue={props.setValue}/>;
  if (["issue-labels","project-labels","project-statuses"].includes(page)) return <ListSettings page={page} data={props.data} settings={props.settings} setSettings={props.setSettings}/>;
  if (page === "issue-templates") return <TemplateSettings data={props.data} type="issue" onReload={props.onReload}/>;
  if (page === "project-templates") return <TemplateSettings data={props.data} type="project" onReload={props.onReload}/>;
  if (page === "sla") return <SLASettings data={props.data} onReload={props.onReload}/>;
  if (page === "project-updates") return <ProjectUpdateSettings data={props.data} onReload={props.onReload}/>;
  if (page === "workspace") return <WorkspacePage {...props}/>;
  if (page === "teams") return <TeamsPage data={props.data} onCreate={props.onCreateTeam} onOpen={team => props.onNavigate("team", team.key)}/>;
  if (page === "members") return <MembersPage data={props.data} onReload={props.onReload}/>;
  if (page === "api") return <ApiPage settings={props.settings} setSettings={props.setSettings}/>;
  if (page === "applications") return <ApplicationsPage settings={props.settings} setSettings={props.setSettings}/>;
  if (page === "billing") return <BillingPage data={props.data}/>;
  if (page === "usage") return <UsagePage data={props.data}/>;
  if (page === "import-export") return <ImportExportSettings data={props.data} onReload={props.onReload}/>;
  if (page === "team") { const team = props.data.teams.find(team => team.key.toLowerCase() === props.teamKey?.toLowerCase()); return team ? <TeamWorkflowSettings data={props.data} team={team} section={props.teamSection ?? "general"} onNavigate={section => props.onNavigate("team", team.key, section)} onReload={props.onReload}/> : <div className="settings-empty"><h3>Team not found</h3></div> }
  if (page === "security") return <SecurityPage values={props.settings.values} setValue={props.setValue}/>;
  if (page === "documents" || page === "customer-requests" || page === "releases" || page === "asks") return <FeatureSettings feature={page}/>;
  return <FeaturePage page={page} values={props.settings.values} setValue={props.setValue}/>;
}

function PageTitle({ children, description, action }: { children: ReactNode; description?: ReactNode; action?: ReactNode }) {
  return <header className="settings-page-header"><div><h1>{children}</h1>{description && <p>{description}</p>}</div>{action}</header>;
}
function Section({ title, children }: { title?: string; children: ReactNode }) { return <section className="settings-section">{title && <h3>{title}</h3>}<div className="settings-card">{children}</div></section>; }
function Row({ title, description, children, danger }: { title: string; description?: ReactNode; children?: ReactNode; danger?: boolean }) { return <div className={`settings-row${danger ? " danger" : ""}`}><div><strong>{title}</strong>{description && <span>{description}</span>}</div>{children && <div className="settings-control">{children}</div>}</div>; }
function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (value: boolean) => void; label: string }) { return <button type="button" role="switch" aria-label={label} aria-checked={checked} className="settings-toggle" onClick={() => onChange(!checked)}><span/></button>; }
function Select({ label, value, options, onChange }: { label: string; value: string; options: string[]; onChange: (value: string) => void }) { return <DropdownMenu><DropdownMenuTrigger asChild><button className="settings-select" aria-label={label}>{value}<ChevronDown size={13}/></button></DropdownMenuTrigger><DropdownMenuContent align="end" className="settings-select-menu">{options.map(option => <DropdownMenuItem key={option} onSelect={() => onChange(option)}>{option}<Check size={13} className={option === value ? "selected-check" : "hidden-check"}/></DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>; }
function ActionButton({ children, onClick, danger, primary, disabled }: { children: ReactNode; onClick?: () => void; danger?: boolean; primary?: boolean; disabled?: boolean }) { return <button disabled={disabled} className={`settings-action${danger ? " danger" : ""}${primary ? " primary" : ""}`} onClick={onClick}>{children}</button>; }

function Preferences({ values, setValue }: { values: StoredSettings["values"]; setValue: (key: string, value: string | boolean) => void }) { const { locale, setLocale } = useI18n(); return <><PageTitle>Preferences</PageTitle><Section title="General">
  <Row title="Language" description="Choose the language used throughout the application"><Select label="Language" value={locale === 'zh-CN' ? '简体中文' : 'English'} options={["English","简体中文"]} onChange={value => { const next = value === '简体中文' ? 'zh-CN' : 'en-US'; setLocale(next); setValue('language', next); }}/></Row>
  <Row title="Default home view" description="Select which view to display when launching Flow"><Select label="Default home view" value={String(values.homeView)} options={["Flow Agent (default)","Inbox","My issues"]} onChange={value => setValue("homeView", value)}/></Row>
  <Row title="Display names" description="Select how names are displayed in the Flow interface"><Select label="Display names" value={String(values.displayNames)} options={["Full name","First name","Username"]} onChange={value => setValue("displayNames", value)}/></Row>
  <Row title="First day of the week" description="Used for date pickers"><Select label="First day of the week" value={String(values.firstDay)} options={["Monday","Saturday","Sunday"]} onChange={value => setValue("firstDay", value)}/></Row>
  <Row title="Convert text emoticons into emojis" description="Strings like :) will be converted to 🙂"><Toggle label="Convert text emoticons into emojis" checked={Boolean(values.emoticons)} onChange={value => setValue("emoticons", value)}/></Row>
  <Row title="Send comments on…" description="Choose which key press is used to submit comments"><Select label="Send comments on" value={String(values.sendComments)} options={["Enter","⌘ Enter"]} onChange={value => setValue("sendComments", value)}/></Row>
  </Section><Section title="Interface and theme">
  <Row title="App sidebar" description="Customize sidebar item visibility, ordering, and badge style"><ActionButton onClick={() => toast("Sidebar customization saved")}>Customize</ActionButton></Row>
  <Row title="Font size" description="Adjust the size of text across the app"><Select label="Font size" value={String(values.fontSize)} options={["Small","Default","Large"]} onChange={value => setValue("fontSize", value)}/></Row>
  <Row title="Use pointer cursors" description="Change the cursor when hovering over interactive elements"><Toggle label="Use pointer cursors" checked={Boolean(values.pointerCursor)} onChange={value => setValue("pointerCursor", value)}/></Row>
  <Row title="Underline links" description="Always underline links in text content"><Toggle label="Underline links" checked={Boolean(values.underlineLinks)} onChange={value => setValue("underlineLinks", value)}/></Row>
  <Row title="Interface theme" description="Select or customize your interface color scheme"><Select label="Interface theme" value={String(values.interfaceTheme)} options={["System preference","Light","Dark"]} onChange={value => setValue("interfaceTheme", value)}/></Row>
  <Row title="Light" description="Theme to use for light system appearance"><Select label="Light theme" value={String(values.lightTheme)} options={["Light","Light high contrast"]} onChange={value => setValue("lightTheme", value)}/></Row>
  <Row title="Dark" description="Theme to use for dark system appearance"><Select label="Dark theme" value={String(values.darkTheme)} options={["Dark","Dark high contrast","Midnight"]} onChange={value => setValue("darkTheme", value)}/></Row>
  </Section><Section title="Desktop application"><Row title="Open in desktop app" description="Automatically open links in desktop app when possible"><Toggle label="Open in desktop app" checked={Boolean(values.desktopLinks)} onChange={value => setValue("desktopLinks", value)}/></Row></Section>
  <Section title="Automations and workflows"><Row title="Auto-assign to self" description="When creating new issues, always assign them to yourself by default"><Toggle label="Auto-assign to self" checked={Boolean(values.autoAssign)} onChange={value => setValue("autoAssign", value)}/></Row><Row title="On move to started status, assign to yourself" description="Automatically assign unassigned issues moved to started"><Toggle label="Assign moved issues" checked={Boolean(values.assignStarted)} onChange={value => setValue("assignStarted", value)}/></Row></Section></>; }

function ProfilePage({ data, values, setValue }: { data: BootstrapData; values: StoredSettings["values"]; setValue: (key: string, value: string | boolean) => void }) {
  return <><PageTitle>Profile</PageTitle><Section><Row title="Profile picture"><button className="settings-avatar" title="Upload profile photo">{initials(data.viewer.displayName)}<Upload size={13}/></button></Row><Row title="Email"><span className="settings-static">{data.viewer.email}</span><ActionButton onClick={() => toast("Email change link sent")}>Change email</ActionButton></Row><FieldRow title="Full name" value={String(values.fullName ?? data.viewer.displayName)} onCommit={value => setValue("fullName", value)}/><FieldRow title="Title" description="Your job title or role" value={String(values.jobTitle ?? "Software engineer")} onCommit={value => setValue("jobTitle", value)}/><FieldRow title="Username" description="One word, like a nickname or first name" value={String(values.username ?? data.viewer.name)} onCommit={value => setValue("username", value)}/></Section><Section title="Workspace access"><Row title="Remove yourself from workspace"><ActionButton danger onClick={() => toast("Workspace owners cannot leave without assigning an owner")}>Leave workspace</ActionButton></Row></Section></>;
}

function FieldRow({ title, description, value, onCommit }: { title: string; description?: string; value: string; onCommit: (value: string) => void }) { const [draft, setDraft] = useState(value); useEffect(() => setDraft(value), [value]); return <Row title={title} description={description}><input className="settings-input" aria-label={title} value={draft} onChange={event => setDraft(event.target.value)} onBlur={() => onCommit(draft)}/></Row>; }

type SettingsValueProps = { values: StoredSettings["values"]; setValue: (key: string, value: string | boolean) => void };
function CodeReviews({ values, setValue }: SettingsValueProps) { return <><PageTitle description="Configure issue links, branches, and pull request automation.">Code & reviews</PageTitle><Section title="Git workflow"><FieldRow title="Branch name format" description="Variables: {identifier} and {title}" value={String(values.branchFormat)} onCommit={value => setValue("branchFormat", value)}/><Row title="Auto-assign review issues" description="Assign linked issues when you open a pull request"><Toggle label="Auto-assign review issues" checked={Boolean(values.reviewAutoAssign)} onChange={value => setValue("reviewAutoAssign", value)}/></Row></Section><Section title="Pull request links"><Row title="GitHub" description="Connect GitHub to link commits and pull requests"><ActionButton onClick={() => setValue("github", !values.github)}>{values.github ? "Disconnect" : "Connect"}</ActionButton></Row></Section></>; }
function AccountSecurity({ values, setValue }: SettingsValueProps) { return <><PageTitle>Security & access</PageTitle><Section title="Authentication"><Row title="Passkeys" description="Use a passkey to securely sign in without a password"><Toggle label="Passkeys" checked={Boolean(values.passkeys)} onChange={value => setValue("passkeys", value)}/></Row><Row title="Password" description="Last changed: never"><ActionButton onClick={() => toast("Password reset email sent")}>Change password</ActionButton></Row></Section><Section title="Active sessions"><Row title="Chrome on macOS" description="Shanghai, China · Current session"><ActionButton danger onClick={() => toast("Other sessions signed out")}>Sign out others</ActionButton></Row></Section></>; }
function Connections({ values, setValue }: SettingsValueProps) { return <><PageTitle description="Manage accounts used to sign in and connect your work.">Connected accounts</PageTitle><Section><ConnectionRow name="Google" connected={Boolean(values.google)} onToggle={() => setValue("google", !values.google)}/><ConnectionRow name="GitHub" connected={Boolean(values.github)} onToggle={() => setValue("github", !values.github)}/><ConnectionRow name="Slack" connected={Boolean(values.slack)} onToggle={() => setValue("slack", !values.slack)}/></Section></>; }
function ConnectionRow({ name, connected, onToggle }: { name: string; connected: boolean; onToggle: () => void }) { return <Row title={name} description={connected ? "Connected" : `Connect your ${name} account`}><ActionButton onClick={onToggle}>{connected ? "Disconnect" : "Connect"}</ActionButton></Row>; }
function Agents({ values, setValue }: SettingsValueProps) { const [draft, setDraft] = useState(String(values.agentInstructions)); return <><PageTitle description="Personalize how agents work with you across Flow.">Agent personalization</PageTitle><Section title="Personal instructions"><div className="settings-editor"><textarea aria-label="Agent instructions" placeholder="Add preferences, context, or instructions for agents…" value={draft} onChange={event => setDraft(event.target.value)} onBlur={() => setValue("agentInstructions", draft)}/><footer><span>{draft.length}/4000</span><ActionButton primary disabled={draft === values.agentInstructions} onClick={() => setValue("agentInstructions", draft)}>Save</ActionButton></footer></div><Row title="Enable personalization" description="Apply these instructions to all agents"><Toggle label="Enable personalization" checked={Boolean(values.agentEnabled)} onChange={value => setValue("agentEnabled", value)}/></Row></Section></>; }

const LIST_TITLES: Partial<Record<SettingsPageId, { title: string; empty: string; create: string; key: string }>> = {
  "issue-labels": { title: "Issue labels", empty: "No issue labels", create: "New label", key: "issueLabels" },
  "issue-templates": { title: "Issue templates", empty: "No issue templates", create: "New template", key: "issueTemplates" },
  "project-labels": { title: "Project labels", empty: "No project labels", create: "New label", key: "projectLabels" },
  "project-templates": { title: "Project templates", empty: "No project templates", create: "New template", key: "projectTemplates" },
  "project-statuses": { title: "Project statuses", empty: "No project statuses", create: "New status", key: "projectStatuses" },
};
function ListSettings({ page, data, settings, setSettings }: { page: SettingsPageId; data: BootstrapData; settings: StoredSettings; setSettings: React.Dispatch<React.SetStateAction<StoredSettings>> }) {
  const config = LIST_TITLES[page]!;
  const initial = page === "issue-labels" ? data.labels.map(label => ({ id: label.id, name: label.name, description: label.description, color: label.color })) : page === "project-statuses" ? data.projectStatuses.map(status => ({ id: status.id, name: status.name, description: status.type, color: status.color })) : [];
  const items = settings.lists[config.key] ?? initial;
  const [query, setQuery] = useState("");
  const update = (next: SettingListItem[]) => setSettings(current => ({ ...current, lists: { ...current.lists, [config.key]: next } }));
  const create = () => update([...items, { id: crypto.randomUUID(), name: config.create.replace("New ", "Untitled "), color: "#8b8d98" }]);
  return <><PageTitle action={<ActionButton primary onClick={create}><Plus size={14}/>{config.create}</ActionButton>}>{config.title}</PageTitle><div className="settings-list-toolbar"><Search size={14}/><input aria-label={`Search ${config.title}`} placeholder="Search…" value={query} onChange={event => setQuery(event.target.value)}/></div><Section>{items.filter(item => item.name.toLowerCase().includes(query.toLowerCase())).map(item => <div className="settings-list-row" key={item.id}><i style={{ background: item.color }}/><input aria-label={`${item.name} name`} value={item.name} onChange={event => update(items.map(current => current.id === item.id ? { ...current, name: event.target.value } : current))}/><span>{item.description}</span><DropdownMenu><DropdownMenuTrigger asChild><button aria-label={`Open ${item.name} menu`}><MoreHorizontal size={15}/></button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem onSelect={() => update(items.filter(current => current.id !== item.id))} className="danger-item"><Trash2 size={14}/>Delete</DropdownMenuItem></DropdownMenuContent></DropdownMenu></div>)}{!items.length && <div className="settings-empty"><Tag size={28}/><h3>{config.empty}</h3><p>Create one to standardize work across your workspace.</p><ActionButton primary onClick={create}>{config.create}</ActionButton></div>}</Section></>;
}

function WorkspacePage(props: SettingsPageProps & { settings: StoredSettings; setSettings: React.Dispatch<React.SetStateAction<StoredSettings>>; setValue: (key: string, value: string | boolean) => void }) { const [name, setName] = useState(props.data.workspace.name); const [urlKey, setUrlKey] = useState(props.data.workspace.urlKey); const [confirm, setConfirm] = useState(false); const save = async () => { await props.onWorkspaceUpdate({ name, urlKey }); toast.success("Workspace saved"); }; return <><PageTitle>Workspace</PageTitle><Section><Row title="Logo" description="Recommended size is 256x256px"><button className="settings-workspace-logo">{initials(name)}<Upload size={13}/></button></Row><Row title="Name"><input className="settings-input" aria-label="Name" value={name} onChange={event => setName(event.target.value)} onBlur={save}/></Row><Row title="URL"><div className="settings-url"><span>flow.app/</span><input aria-label="URL" value={urlKey} onChange={event => setUrlKey(slug(event.target.value))} onBlur={save}/></div></Row></Section><Section title="Time & region"><Row title="First month of the fiscal year" description="Used when grouping projects and issues quarterly, half-yearly, and yearly"><Select label="First month of the fiscal year" value={String(props.settings.values.fiscalMonth ?? "January")} options={["January","February","March","April","May","June","July","August","September","October","November","December"]} onChange={value => props.setValue("fiscalMonth", value)}/></Row><Row title="Region" description="Set when a workspace is created and cannot be changed."><span className="settings-static">{props.data.workspace.region === "eu" ? "European Union" : "United States"}</span></Row></Section><Section title="Member onboarding"><Row title="Welcome message"><span className="settings-enterprise">Available on Enterprise</span></Row><Row title="Default home view"><span className="settings-enterprise">Available on Enterprise</span></Row></Section><Section title="Danger zone"><Row danger title="Delete workspace" description="Schedule workspace to be permanently deleted"><ActionButton danger onClick={() => setConfirm(true)}>Delete workspace</ActionButton></Row></Section><ConfirmDialog open={confirm} title="Delete workspace?" description={`This permanently deletes ${props.data.workspace.name} and all of its data.`} confirm="Delete workspace" onCancel={() => setConfirm(false)} onConfirm={async () => { setConfirm(false); await props.onWorkspaceDelete(); }}/></>; }
function ConfirmDialog({ open, title, description, confirm, onCancel, onConfirm }: { open: boolean; title: string; description: string; confirm: string; onCancel: () => void; onConfirm: () => void }) { return <Dialog open={open} onOpenChange={value => !value && onCancel()}><DialogContent className="settings-confirm"><DialogTitle>{title}</DialogTitle><p>{description}</p><footer><ActionButton onClick={onCancel}>Cancel</ActionButton><ActionButton danger onClick={onConfirm}>{confirm}</ActionButton></footer></DialogContent></Dialog>; }

function download(filename: string, content: string, type: string) {
  const url = URL.createObjectURL(new Blob([content], { type }));
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}

function TeamsPage({ data, onCreate, onOpen }: { data: BootstrapData; onCreate: () => void; onOpen: (team: Team) => void }) { return <><PageTitle description="Teams organize issues, projects, cycles, and views." action={<ActionButton primary onClick={onCreate}><Plus size={14}/>New team</ActionButton>}>Teams</PageTitle><Section>{data.teams.map(team => <button className="settings-team-row" key={team.id} onClick={() => onOpen(team)}><span className="settings-team-icon" style={{ color: team.color }}>{team.icon || team.key[0]}</span><div><strong>{team.name}</strong><span>{team.key} · {data.issues.filter(issue => issue.team.id === team.id).length} issues</span></div><ChevronDown size={14}/></button>)}</Section></>; }
function MembersPage({ data, onReload }: { data: BootstrapData; onReload: () => Promise<void> }) {
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("All");
  const [inviteOpen, setInviteOpen] = useState(false);
  const [emails, setEmails] = useState("");
  const [role, setRole] = useState<"admin"|"member"|"guest">("member");
  const [teamIds, setTeamIds] = useState<string[]>(data.teams[0] ? [data.teams[0].id] : []);
  const [busy, setBusy] = useState(false);
  const members = data.members.filter(member => (status === "All" || member.status === status.toLowerCase()) && `${member.user.displayName} ${member.user.email}`.toLowerCase().includes(query.toLowerCase()));
  const pending = data.invitations.filter(invitation => invitation.status === "pending" && (status === "All" || status === "Invited") && invitation.email.toLowerCase().includes(query.toLowerCase()));
  const change = async (action: () => Promise<unknown>, success: string) => { setBusy(true); try { await action(); await onReload(); toast.success(success); } catch (error) { toast.error(error instanceof Error ? error.message : "Could not update member"); } finally { setBusy(false); } };
  const exportCsv = () => download("members.csv", ["Name,Email,Status,Role", ...data.members.map(member => `${member.user.displayName},${member.user.email},${member.status},${member.role}`)].join("\n"), "text/csv");
  const send = () => change(async () => {
    const parsed = emails.split(/[\s,;]+/).map(value => value.trim()).filter(Boolean);
    if (!parsed.length) throw new Error("Enter at least one email address");
    if (role === "guest" && !teamIds.length) throw new Error("Guests must be assigned to a team");
    const invitations = await inviteMembers(data.workspace.urlKey, { emails: parsed, role, teamIds });
    const token = invitations.find(item => item.token)?.token;
    if (token) await navigator.clipboard?.writeText(`${location.origin}/invite/${token}`);
    setInviteOpen(false); setEmails("");
  }, "Invitation sent");
  return <><PageTitle action={<div className="settings-header-actions"><ActionButton onClick={exportCsv}>Export CSV</ActionButton><ActionButton primary onClick={() => setInviteOpen(true)}><Plus size={14}/>Invite</ActionButton></div>}>Members</PageTitle>
    <div className="settings-members-toolbar"><label><Search size={14}/><input aria-label="Search by name or email" placeholder="Search by name or email" value={query} onChange={event => setQuery(event.target.value)}/></label><Select label="Member status" value={status} options={["All","Active","Invited","Suspended"]} onChange={setStatus}/></div>
    <div className="settings-member-groups">
      {members.length > 0 && <section><h3>{status === "Suspended" ? "Suspended" : "Active"}<span>{members.length}</span></h3><div className="settings-members-table"><header><span>Name</span><span>Email</span><span>Role</span><span>Teams</span><span>Joined</span><span/></header>{members.map(member => {
        const memberships = data.teamMembers.filter(item => item.userId === member.user.id);
        return <div key={member.user.id}><span><b className="settings-member-avatar">{initials(member.user.displayName)}</b><i><strong>{member.user.displayName}</strong>{member.user.id === data.viewer.id && <small>You</small>}</i></span><span>{member.user.email}</span><span><Select label={`Role for ${member.user.displayName}`} value={title(member.role)} options={["Admin","Member","Guest"]} onChange={value => void change(() => updateMemberRole(data.workspace.urlKey, member.user.id, value.toLowerCase() as "admin"|"member"|"guest"), "Role updated")}/></span><span><DropdownMenu><DropdownMenuTrigger asChild><button className="settings-member-teams">{memberships.length ? `${memberships.length} team${memberships.length === 1 ? "" : "s"}` : "No teams"}<ChevronDown size={12}/></button></DropdownMenuTrigger><DropdownMenuContent align="end" className="settings-team-membership-menu">{data.teams.map(team => { const membership = memberships.find(item => item.teamId === team.id); return <DropdownMenuCheckboxItem key={team.id} checked={Boolean(membership)} onCheckedChange={checked => void change(() => setTeamMembership(data.workspace.urlKey, team.id, member.user.id, Boolean(checked), membership?.role ?? "member"), "Team access updated")}>{team.name}</DropdownMenuCheckboxItem>; })}</DropdownMenuContent></DropdownMenu></span><span>{formatDate(member.joinedAt)}</span><span><DropdownMenu><DropdownMenuTrigger asChild><button className="settings-member-more" aria-label={`Actions for ${member.user.displayName}`}><MoreHorizontal size={15}/></button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem disabled={member.user.id === data.viewer.id} onSelect={() => void change(() => suspendMember(data.workspace.urlKey, member.user.id), "Member suspended")}>Suspend member</DropdownMenuItem><DropdownMenuSeparator/><DropdownMenuItem className="danger-item" disabled={member.user.id === data.viewer.id} onSelect={() => void change(() => removeMember(data.workspace.urlKey, member.user.id), "Member removed")}>Remove from workspace</DropdownMenuItem></DropdownMenuContent></DropdownMenu></span></div>})}</div></section>}
      {pending.length > 0 && <section><h3>Invited<span>{pending.length}</span></h3><div className="settings-members-table pending"><header><span>Email</span><span>Role</span><span>Teams</span><span>Invited</span><span/></header>{pending.map(invitation => <div key={invitation.id}><span>{invitation.email}</span><span>{title(invitation.role)}</span><span>{invitation.teamIds.length} team{invitation.teamIds.length === 1 ? "" : "s"}</span><span>{formatDate(invitation.createdAt)}</span><span><DropdownMenu><DropdownMenuTrigger asChild><button className="settings-member-more"><MoreHorizontal size={15}/></button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem onSelect={() => void change(() => resendInvitation(data.workspace.urlKey, invitation.id), "Invitation resent")}>Resend invite</DropdownMenuItem><DropdownMenuItem className="danger-item" onSelect={() => void change(() => revokeInvitation(data.workspace.urlKey, invitation.id), "Invitation revoked")}>Revoke invite</DropdownMenuItem></DropdownMenuContent></DropdownMenu></span></div>)}</div></section>}
      {!members.length && !pending.length && <div className="settings-empty compact"><UserRound size={24}/><h3>No members found</h3><p>Try another name, email, or member status.</p></div>}
    </div>
    <Dialog open={inviteOpen} onOpenChange={setInviteOpen}><DialogContent className="settings-invite-dialog"><DialogTitle>Invite to your workspace</DialogTitle><p>Invite teammates to {data.workspace.name}.</p><label>Email addresses<textarea autoFocus placeholder="name@company.com" value={emails} onChange={event => setEmails(event.target.value)}/><small>Separate multiple emails with commas or spaces.</small></label><div className="settings-invite-options"><label>Role<Select label="Invitation role" value={title(role)} options={["Member","Admin","Guest"]} onChange={value => setRole(value.toLowerCase() as "admin"|"member"|"guest")}/></label><label>Teams<DropdownMenu><DropdownMenuTrigger asChild><button className="settings-select">{teamIds.length ? `${teamIds.length} selected` : "Select teams"}<ChevronDown size={13}/></button></DropdownMenuTrigger><DropdownMenuContent align="end">{data.teams.map(team => <DropdownMenuCheckboxItem key={team.id} checked={teamIds.includes(team.id)} onCheckedChange={checked => setTeamIds(current => checked ? [...new Set([...current, team.id])] : current.filter(id => id !== team.id))}>{team.name}</DropdownMenuCheckboxItem>)}</DropdownMenuContent></DropdownMenu></label></div><footer><ActionButton onClick={() => setInviteOpen(false)}>Cancel</ActionButton><ActionButton primary disabled={busy} onClick={send}>Send invites</ActionButton></footer></DialogContent></Dialog>
  </>;
}

function SecurityPage({ values, setValue }: SettingsValueProps) { return <><PageTitle description="Manage authentication and access policies for your workspace.">Security</PageTitle><Section title="Access"><Row title="Allow guest accounts" description="Guests only see issues shared with their teams"><Toggle label="Allow guest accounts" checked={Boolean(values.guests ?? true)} onChange={value => setValue("guests", value)}/></Row><Row title="Require two-factor authentication" description="Require all members to secure their account"><Toggle label="Require two-factor authentication" checked={Boolean(values.require2fa)} onChange={value => setValue("require2fa", value)}/></Row><Row title="Session duration" description="How long members stay signed in"><Select label="Session duration" value={String(values.sessionDuration ?? "30 days")} options={["7 days","30 days","90 days"]} onChange={value => setValue("sessionDuration", value)}/></Row></Section><Section title="Domains"><FieldRow title="Allowed email domains" description="Separate domains with commas" value={String(values.allowedDomains ?? "")} onCommit={value => setValue("allowedDomains", value)}/></Section></>; }
function ApiPage({ settings, setSettings }: { settings: StoredSettings; setSettings: React.Dispatch<React.SetStateAction<StoredSettings>> }) { const key = "apiKeys"; const items = settings.lists[key] ?? []; const create = () => { const item = { id: crypto.randomUUID(), name: `lin_api_${Math.random().toString(36).slice(2,10)}`, description: "Created just now" }; setSettings(current => ({ ...current, lists: { ...current.lists, [key]: [...items, item] } })); toast.success("API key created. Store it securely."); }; return <><PageTitle description="API keys grant access to your Flow workspace." action={<ActionButton primary onClick={create}><Plus size={14}/>New API key</ActionButton>}>API</PageTitle><Section>{items.map(item => <Row key={item.id} title={item.name} description={item.description}><ActionButton danger onClick={() => setSettings(current => ({ ...current, lists: { ...current.lists, [key]: items.filter(value => value.id !== item.id) } }))}>Revoke</ActionButton></Row>)}{!items.length && <div className="settings-empty"><Braces size={28}/><h3>No personal API keys</h3><p>Create an API key to access the Flow API.</p><ActionButton primary onClick={create}>Create key</ActionButton></div>}</Section></>; }
function ApplicationsPage({ settings, setSettings }: { settings: StoredSettings; setSettings: React.Dispatch<React.SetStateAction<StoredSettings>> }) { const items = settings.lists.oauthApps ?? []; const create = () => setSettings(current => ({ ...current, lists: { ...current.lists, oauthApps: [...items, { id: crypto.randomUUID(), name: "Untitled application", description: "OAuth application" }] } })); return <><PageTitle description="Build applications that integrate with Flow." action={<ActionButton primary onClick={create}><Plus size={14}/>New application</ActionButton>}>Applications</PageTitle><Section>{items.map(item => <Row key={item.id} title={item.name} description={item.description}><ActionButton>Configure</ActionButton></Row>)}{!items.length && <div className="settings-empty"><AppWindow size={28}/><h3>No applications</h3><p>Create an OAuth application to integrate with Flow.</p><ActionButton primary onClick={create}>New application</ActionButton></div>}</Section></>; }
function BillingPage({ data }: { data: BootstrapData }) { return <><PageTitle>Billing</PageTitle><Section title="Current plan"><Row title="Free" description={`${data.users.length} members · $0 per month`}><ActionButton primary onClick={() => toast("Plan picker opened")}>Upgrade</ActionButton></Row><Row title="Billing email" description={data.viewer.email}><ActionButton>Change</ActionButton></Row></Section><Section title="Invoices"><div className="settings-empty compact"><CreditCard size={24}/><h3>No invoices yet</h3><p>Invoices will appear here after your first paid billing period.</p></div></Section></>; }
function UsagePage({ data }: { data: BootstrapData }) { return <><PageTitle description="Current workspace usage for this billing period.">Usage & limits</PageTitle><Section><UsageRow title="Members" value={data.users.length} limit={250}/><UsageRow title="Issues" value={data.issues.length} limit={250}/><UsageRow title="File storage" value={Math.max(1, data.issues.flatMap(issue => issue.attachments).length)} limit={100}/></Section></>; }
function UsageRow({ title, value, limit }: { title: string; value: number; limit: number }) { return <div className="settings-usage"><div><strong>{title}</strong><span>{value} of {limit}</span></div><i><b style={{ width: `${Math.min(100, value / limit * 100)}%` }}/></i></div>; }

const FEATURE_COPY: Partial<Record<SettingsPageId, { title: string; description: string; rows: [string,string][] }>> = {
  sla: { title: "SLAs", description: "Set response and resolution targets for issues.", rows: [["Enable SLAs","Track service level agreements across issue views"],["Show SLA countdown","Display remaining time on issue rows"]] },
  "project-updates": { title: "Project updates", description: "Configure reminders and health update cadence.", rows: [["Update reminders","Remind project leads to post updates"],["Missed update notifications","Notify members when an update is overdue"]] },
  ai: { title: "AI & Agents", description: "Configure AI features and agents for your workspace.", rows: [["Enable AI features","Allow members to use Flow AI"],["Agent sessions","Allow agents to work on assigned issues"]] },
  initiatives: { title: "Initiatives", description: "Organize projects into workspace initiatives.", rows: [["Enable initiatives","Show initiatives in the workspace sidebar"]] },
  documents: { title: "Documents", description: "Create collaborative documents inside projects.", rows: [["Enable documents","Allow workspace members to create documents"]] },
  "customer-requests": { title: "Customer requests", description: "Connect customer feedback to product work.", rows: [["Enable customer requests","Track customer needs on issues and projects"]] },
  releases: { title: "Releases", description: "Coordinate product releases across teams.", rows: [["Enable releases","Show releases in project planning"]] },
  pulse: { title: "Pulse", description: "Share and discover project and initiative updates.", rows: [["Enable Pulse","Show Pulse in the workspace sidebar"]] },
  asks: { title: "Asks", description: "Turn requests from connected tools into Flow issues.", rows: [["Enable Asks","Allow members to create issues through Asks"]] },
  emojis: { title: "Emojis", description: "Manage custom emoji available in your workspace.", rows: [["Custom emoji","Allow members to add workspace emoji"]] },
  integrations: { title: "Integrations", description: "Connect Flow with the tools your team uses.", rows: [["GitHub","Link pull requests and commits to issues"],["Slack","Create and update issues from Slack"],["Figma","Preview design links in issues"]] },
};
function FeaturePage({ page, values, setValue }: { page: SettingsPageId; values: StoredSettings["values"]; setValue: (key: string, value: string | boolean) => void }) { const copy = FEATURE_COPY[page] ?? { title: page, description: "Workspace feature settings.", rows: [["Enable feature","Make this feature available to workspace members"]] as [string,string][] }; return <><PageTitle description={copy.description}>{copy.title}</PageTitle><Section>{copy.rows.map(([title, description]) => { const key = `feature:${page}:${title}`; return <Row key={title} title={title} description={description}>{page === "integrations" ? <ActionButton onClick={() => setValue(key, !values[key])}>{values[key] ? "Disconnect" : "Connect"}</ActionButton> : <Toggle label={title} checked={Boolean(values[key])} onChange={value => setValue(key, value)}/>}</Row>; })}</Section></>; }

function useStoredSettings(workspace: string, remote: Record<string, unknown> | undefined, persist: (settings: Record<string, unknown>) => Promise<Record<string, unknown>>) { const key = `flow:settings:${workspace}`; const [state, setState] = useState<StoredSettings>(() => { try { const local = JSON.parse(localStorage.getItem(key) ?? "{}"); const source = Object.keys(remote ?? {}).length ? remote as Partial<StoredSettings> : local; return { values: { ...DEFAULT_VALUES, ...(source.values ?? {}) }, lists: source.lists ?? {} }; } catch { return { values: DEFAULT_VALUES, lists: {} }; } }); useEffect(() => { localStorage.setItem(key, JSON.stringify(state)); const timeout = window.setTimeout(() => { void persist(state).catch(() => toast.error("Could not save settings")); }, 250); return () => window.clearTimeout(timeout); }, [key, persist, state]); return [state, setState] as const; }
function initials(value: string) { return value.split(/\s+/).filter(Boolean).map(part => part[0]).join("").slice(0,2).toUpperCase() || "W"; }
function title(value: string) { return value ? value[0].toUpperCase() + value.slice(1) : value; }
function formatDate(value: string) { return new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", year: "numeric" }).format(new Date(value)); }
function slug(value: string) { return value.toLowerCase().replace(/[^a-z0-9-]+/g,"-").replace(/^-+|-+$/g,"").slice(0,32); }
