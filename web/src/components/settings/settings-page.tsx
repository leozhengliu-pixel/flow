import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  Activity, AppWindow, ArrowLeft, Bell, Bot, Braces, Building2, Check, ChevronDown,
  CircleDot, Code2, CreditCard, FileText, Flame, Gauge,
  Import, KeyRound, LayoutTemplate, Link2, ListFilter, MessageCircleQuestion,
  MoreHorizontal, PanelTop, Plug, Plus, Radio, Rocket, Search, ShieldCheck,
  SlidersHorizontal, Smile, Sparkles, Tag, UserRound, UsersRound,
  X, Zap, type LucideIcon,
} from "lucide-react";
import { toast } from "sonner";

import {
  DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { connectIntegration, createAPIKey, createOAuthApplication, createWebhook, deleteOAuthApplication, deleteWebhook, disconnectIntegration, fetchWorkspaceUsage, revokeAPIKey, inviteMembers, removeMember, resendInvitation, revokeInvitation, setTeamMembership, suspendMember, updateMemberRole, updateOAuthApplication, updateUserSettings, updateWebhook, updateWorkspacePreferences } from "@/lib/api";
import type { SettingsPageId, TeamSettingsSection } from "@/lib/app-routes";
import type { BootstrapData, OAuthApplication, Team, UserSettings, Webhook, WorkspaceMutationInput, WorkspaceSettings } from "@/types/flow";
import { TeamWorkflowSettings } from "./team-workflow-settings";
import { ImportExportSettings, ProjectUpdateSettings, SLASettings, TemplateSettings } from "./advanced-settings";
import { DomainLabelsSettings, ProjectStatusesSettings } from "./domain-settings";
import { FeatureSettingsPage } from "./feature-settings";

import "./settings.css";
import "./workflow-settings.css";
import "./advanced-settings.css";
import { applyTheme } from "@/lib/theme";
import { PersonalSettings } from "./personal-settings";

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
  codeReviewsEnabled: true, autoConvertDrafts: false, mergeStrategy: "Squash and merge",
  codeTheme: "Flow Light", codeFont: "12px, Regular, Default", reviewCommentsFilter: "Exclude Bots",
  reviewRequests: true, githubTeamReviewRequests: true, checksMergeQueue: true,
  requireSignedCommits: false, gitAttachmentFormat: "Title", gitBranchMoveStarted: true,
  codingToolMoveStarted: true, changelogUpdates: true, changelogNewsletter: false,
  marketingUpdates: false, inviteAcceptedUpdates: true, privacyUpdates: true, dpaUpdates: false,
  passkeys: false, agentEnabled: true, agentInstructions: "",
};

export function SettingsPage(props: SettingsPageProps) {
  const [query, setQuery] = useState("");
  const [mobileNav, setMobileNav] = useState(false);
  const [settings, setSettings] = useUserStoredSettings(props.data);
  const isAdmin = props.data.viewerRole === "admin";
  const visible = useMemo(() => NAV.map(section => ({
    ...section,
    items: section.items.filter(item => (isAdmin || section.title === "Personal" || memberCanManage(item.id, props.data.workspaceSettings)) && item.label.toLowerCase().includes(query.toLowerCase())),
  })).filter(section => section.items.length), [isAdmin, props.data.workspaceSettings, query]);
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
  if (!personal && props.data.viewerRole !== "admin" && !teamOwner && !memberCanManage(page,props.data.workspaceSettings)) return <div className="settings-empty"><ShieldCheck size={28}/><h3>Admin access required</h3><p>You don't have permission to manage this workspace setting.</p></div>;
  if (personal) return <PersonalSettings page={page} data={props.data} values={props.settings.values} setValue={props.setValue} onNavigate={props.onNavigate} onReload={props.onReload} onBack={props.onBack}/>;
  if (page === "issue-labels") return <DomainLabelsSettings data={props.data} resourceType="issue" onReload={props.onReload}/>;
  if (page === "project-labels") return <DomainLabelsSettings data={props.data} resourceType="project" onReload={props.onReload}/>;
  if (page === "project-statuses") return <ProjectStatusesSettings data={props.data} onReload={props.onReload}/>;
  if (page === "issue-templates") return <TemplateSettings data={props.data} type="issue" onReload={props.onReload}/>;
  if (page === "project-templates") return <TemplateSettings data={props.data} type="project" onReload={props.onReload}/>;
  if (page === "sla") return <SLASettings data={props.data} onReload={props.onReload}/>;
  if (page === "project-updates") return <ProjectUpdateSettings data={props.data} onReload={props.onReload}/>;
  if (page === "workspace") return <WorkspacePage {...props}/>;
  if (page === "teams") return <TeamsPage data={props.data} onCreate={props.onCreateTeam} onOpen={team => props.onNavigate("team", team.key)}/>;
  if (page === "members") return <MembersPage data={props.data} onReload={props.onReload}/>;
  if (page === "api") return <ApiPage data={props.data} onReload={props.onReload}/>;
  if (page === "applications") return <ApplicationsPage data={props.data} onReload={props.onReload}/>;
  if (page === "billing") return <BillingPage data={props.data} onReload={props.onReload}/>;
  if (page === "usage") return <UsagePage data={props.data}/>;
  if (page === "import-export") return <ImportExportSettings data={props.data} onReload={props.onReload}/>;
  if (page === "team") { const team = props.data.teams.find(team => team.key.toLowerCase() === props.teamKey?.toLowerCase()); return team ? <TeamWorkflowSettings data={props.data} team={team} section={props.teamSection ?? "overview"} onNavigate={section => props.onNavigate("team", team.key, section)} onReload={props.onReload}/> : <div className="settings-empty"><h3>Team not found</h3></div> }
  if (page === "security") return <SecurityPage data={props.data} onReload={props.onReload}/>;
  if (["ai","initiatives","documents","customer-requests","releases","pulse","asks","emojis","integrations"].includes(page)) return <FeatureSettingsPage page={page as "ai"|"initiatives"|"documents"|"customer-requests"|"releases"|"pulse"|"asks"|"emojis"|"integrations"} data={props.data} onReload={props.onReload}/>;
  return <FeaturePage page={page} data={props.data} onReload={props.onReload}/>;
}

function PageTitle({ children, description, action }: { children: ReactNode; description?: ReactNode; action?: ReactNode }) {
  return <header className="settings-page-header"><div><h1>{children}</h1>{description && <p>{description}</p>}</div>{action}</header>;
}
function Section({ title, children }: { title?: string; children: ReactNode }) { return <section className="settings-section">{title && <h3>{title}</h3>}<div className="settings-card">{children}</div></section>; }
function Row({ title, description, children, danger }: { title: string; description?: ReactNode; children?: ReactNode; danger?: boolean }) { return <div className={`settings-row${danger ? " danger" : ""}`}><div><strong>{title}</strong>{description && <span>{description}</span>}</div>{children && <div className="settings-control">{children}</div>}</div>; }
function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (value: boolean) => void; label: string }) { return <button type="button" role="switch" aria-label={label} aria-checked={checked} className="settings-toggle" onClick={() => onChange(!checked)}><span/></button>; }
function Select({ label, value, options, onChange }: { label: string; value: string; options: string[]; onChange: (value: string) => void }) { return <DropdownMenu><DropdownMenuTrigger asChild><button className="settings-select" aria-label={label}>{value}<ChevronDown size={13}/></button></DropdownMenuTrigger><DropdownMenuContent align="end" className="settings-select-menu">{options.map(option => <DropdownMenuItem key={option} onSelect={() => onChange(option)}>{option}<Check size={13} className={option === value ? "selected-check" : "hidden-check"}/></DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>; }
function ActionButton({ children, onClick, danger, primary, disabled }: { children: ReactNode; onClick?: () => void; danger?: boolean; primary?: boolean; disabled?: boolean }) { return <button disabled={disabled} className={`settings-action${danger ? " danger" : ""}${primary ? " primary" : ""}`} onClick={onClick}>{children}</button>; }

function FieldRow({ title, description, value, onCommit }: { title: string; description?: string; value: string; onCommit: (value: string) => void }) { const [draft, setDraft] = useState(value); useEffect(() => setDraft(value), [value]); return <Row title={title} description={description}><input className="settings-input" aria-label={title} value={draft} onChange={event => setDraft(event.target.value)} onBlur={() => onCommit(draft)}/></Row>; }

function WorkspacePage(props: SettingsPageProps & { settings: StoredSettings; setSettings: React.Dispatch<React.SetStateAction<StoredSettings>>; setValue: (key: string, value: string | boolean) => void }) { const [name, setName] = useState(props.data.workspace.name); const [urlKey, setUrlKey] = useState(props.data.workspace.urlKey);const[icon,setIcon]=useState(props.data.workspace.icon??''); const [confirm, setConfirm] = useState(false); const save = async () => { await props.onWorkspaceUpdate({ name, urlKey,icon }); toast.success("Workspace saved"); };const saveFiscal=async(value:string)=>{await updateWorkspacePreferences({...props.data.workspaceSettings,fiscalMonth:value});await props.onReload()}; return <><PageTitle>Workspace</PageTitle><Section><Row title="Icon" description="Emoji or short text shown throughout the app"><input className="settings-input short" aria-label="Workspace icon" value={icon} maxLength={4} onChange={event=>setIcon(event.target.value)} onBlur={()=>void save()}/></Row><Row title="Name"><input className="settings-input" aria-label="Name" value={name} onChange={event => setName(event.target.value)} onBlur={save}/></Row><Row title="URL"><div className="settings-url"><span>flow.app/</span><input aria-label="URL" value={urlKey} onChange={event => setUrlKey(slug(event.target.value))} onBlur={save}/></div></Row></Section><Section title="Time & region"><Row title="First month of the fiscal year" description="Used when grouping projects and issues quarterly, half-yearly, and yearly"><Select label="First month of the fiscal year" value={props.data.workspaceSettings.fiscalMonth} options={["January","February","March","April","May","June","July","August","September","October","November","December"]} onChange={value => void saveFiscal(value)}/></Row><Row title="Region" description="Set when a workspace is created and cannot be changed."><span className="settings-static">{props.data.workspace.region === "eu" ? "European Union" : "United States"}</span></Row></Section><Section title="Danger zone"><Row danger title="Delete workspace" description="Schedule workspace to be permanently deleted"><ActionButton danger onClick={() => setConfirm(true)}>Delete workspace</ActionButton></Row></Section><ConfirmDialog open={confirm} title="Delete workspace?" description={`This permanently deletes ${props.data.workspace.name} and all of its data.`} confirm="Delete workspace" onCancel={() => setConfirm(false)} onConfirm={async () => { setConfirm(false); await props.onWorkspaceDelete(); }}/></>; }
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

function SecurityPage({data,onReload}:{data:BootstrapData;onReload:()=>Promise<void>}){const[settings,setSettings]=useState(data.workspaceSettings);useEffect(()=>setSettings(data.workspaceSettings),[data.workspaceSettings]);const save=async(next:WorkspaceSettings)=>{setSettings(next);try{await updateWorkspacePreferences(next);await onReload();toast.success('Security policy saved')}catch(error){setSettings(data.workspaceSettings);toast.error(error instanceof Error?error.message:'Could not save policy')}};const toggle=(title:string,key:keyof WorkspaceSettings,description:string)=><Row title={title} description={description}><Toggle label={title} checked={Boolean(settings[key])} onChange={value=>void save({...settings,[key]:value})}/></Row>;return <><PageTitle description="Manage authentication, permissions, and access policies for your workspace.">Security</PageTitle><Section title="Workspace access">{toggle('Invite links','inviteLinksEnabled','Allow members to invite people with a workspace link.')}<Row title="Allow guest accounts" description="Guest invitations are rejected when this is disabled"><Toggle label="Allow guest accounts" checked={settings.guestsAllowed} onChange={value=>void save({...settings,guestsAllowed:value})}/></Row></Section><Section title="Authentication">{toggle('Sign in with Google','googleAuthEnabled','Allow Google accounts as an authentication method.')}{toggle('Email and password','emailAuthEnabled','Allow members to authenticate with an email and password.')}<Row title="Require two-factor authentication" description="Require a second factor for all members."><Toggle label="Require two-factor authentication" checked={settings.requireTwoFactor} onChange={value=>void save({...settings,requireTwoFactor:value})}/></Row>{toggle('Disable administrator bypass','disableAdminBypass','Apply authentication requirements to workspace administrators.')}<Row title="SAML and SCIM" description="Identity provider provisioning requires the Enterprise plan."><button className="settings-action" disabled>Enterprise</button></Row><Row title="Session duration"><Select label="Session duration" value={`${settings.sessionDurationDays} days`} options={["7 days","30 days","90 days"]} onChange={value=>void save({...settings,sessionDurationDays:Number(value.split(' ')[0])})}/></Row></Section><Section title="Approved domains"><FieldRow title="Allowed email domains" description="Invitations outside these domains are rejected. Separate domains with commas." value={(settings.allowedDomains??[]).join(', ')} onCommit={value=>void save({...settings,allowedDomains:value.split(',').map(item=>item.trim()).filter(Boolean)})}/></Section><Section title="Workspace management"><PermissionRow title="New user invitations" value={settings.invitePermission} onChange={value=>void save({...settings,invitePermission:value})}/><PermissionRow title="Team creation" value={settings.teamCreatePermission} onChange={value=>void save({...settings,teamCreatePermission:value})}/><PermissionRow title="Manage labels" value={settings.labelPermission} onChange={value=>void save({...settings,labelPermission:value})}/><PermissionRow title="Manage templates" value={settings.templatePermission} onChange={value=>void save({...settings,templatePermission:value})}/><PermissionRow title="Manage initiatives" value={settings.initiativePermission??'members'} onChange={value=>void save({...settings,initiativePermission:value})}/><PermissionRow title="Create Loops" value={settings.loopPermission??'members'} onChange={value=>void save({...settings,loopPermission:value})}/><PermissionRow title="Agent guidance" value={settings.agentGuidancePermission??'admins'} onChange={value=>void save({...settings,agentGuidancePermission:value})}/><PermissionRow title="API key creation" value={settings.apiKeyPermission} onChange={value=>void save({...settings,apiKeyPermission:value})}/></Section><Section title="AI, agents, and integrations">{toggle('Prevent guests from using agents','preventGuestAgents','Restrict agent sessions to full workspace members.')}{toggle('Share AI usage data','aiUsageSharing','Share anonymized AI usage to improve workspace insights.')}{toggle('Agent web search','agentWebSearch','Allow agents to search the public web.')}{toggle('External Loop triggers','externalLoopTriggers','Allow connected applications to start Loops.')}{toggle('MCP connectors','mcpConnectorsEnabled','Allow members to connect approved MCP servers.')}</Section></>}
function PermissionRow({title,value,onChange}:{title:string;value:string;onChange:(value:string)=>void}){return <Row title={title}><Select label={title} value={value==='admins'?'Only admins':'All members'} options={['Only admins','All members']} onChange={next=>onChange(next==='Only admins'?'admins':'members')}/></Row>}
function ApiPage({data,onReload}:{data:BootstrapData;onReload:()=>Promise<void>}) {
  const [open,setOpen]=useState(false);const [name,setName]=useState('');const [scopes,setScopes]=useState<string[]>(['read','write']);const [teamIds,setTeamIds]=useState<string[]>([]);const [secret,setSecret]=useState('');const[editingOAuth,setEditingOAuth]=useState<OAuthApplication|null|undefined>(undefined);const[editingWebhook,setEditingWebhook]=useState<Webhook|null|undefined>(undefined)
  const items=data.apiKeys.filter(item=>item.creatorId===data.viewer.id&&!item.revokedAt)
  const submit=async()=>{try{const result=await createAPIKey({name,scopes,teamIds});setSecret(result.secret);await onReload()}catch(error){toast.error(error instanceof Error?error.message:'Could not create API key')}}
  const reset=()=>{setOpen(false);setSecret('');setName('');setTeamIds([]);setScopes(['read','write'])}
  return <><PageTitle description="Developer applications, webhooks, and member API access.">API</PageTitle><Section title="OAuth applications">{data.oauthApplications.map(item=><Row key={item.id} title={item.name} description={`${item.clientId} · ${item.scopes.join(', ')}`}><ActionButton onClick={()=>setEditingOAuth(item)}>Configure</ActionButton></Row>)}{!data.oauthApplications.length&&<div className="settings-empty compact"><AppWindow size={24}/><h3>No OAuth applications</h3><p>Create an application for third-party OAuth access.</p></div>}<div className="settings-section-action"><ActionButton onClick={()=>setEditingOAuth(null)}><Plus size={14}/>New OAuth application</ActionButton></div></Section><Section title="Webhooks">{data.webhooks.map(item=><Row key={item.id} title={item.name} description={`${item.url} · ${item.resourceTypes.join(', ') || 'all resources'}`}><div className="settings-inline-actions"><Toggle label={`${item.name} enabled`} checked={item.enabled} onChange={value=>void updateWebhook(item.id,{enabled:value}).then(onReload)}/><ActionButton onClick={()=>setEditingWebhook(item)}>Configure</ActionButton></div></Row>)}{!data.webhooks.length&&<div className="settings-empty compact"><Radio size={24}/><h3>No webhooks</h3><p>Send workspace events to an HTTPS endpoint.</p></div>}<div className="settings-section-action"><ActionButton onClick={()=>setEditingWebhook(null)}><Plus size={14}/>New webhook</ActionButton></div></Section><Section title="Personal API keys"><Row title="Member API key creation" description="Controlled by the workspace Security policy."><span className="settings-static">{data.workspaceSettings.apiKeyPermission==='admins'?'Admins only':'All members'}</span></Row>{items.map(item=><Row key={item.id} title={item.name} description={`${item.prefix}… · ${item.scopes.join(', ')} · ${item.teamIds.length?`${item.teamIds.length} teams`:'all teams'} · ${item.lastUsedAt?`last used ${formatDate(item.lastUsedAt)}`:'never used'}`}><ActionButton danger onClick={()=>void revokeAPIKey(item.id).then(onReload)}>Revoke</ActionButton></Row>)}{!items.length&&<div className="settings-empty compact"><Braces size={24}/><h3>No personal API keys</h3><p>Create a scoped key to access the Flow API.</p></div>}<div className="settings-section-action"><ActionButton onClick={()=>setOpen(true)}><Plus size={14}/>New API key</ActionButton></div></Section><Dialog open={open} onOpenChange={value=>value?setOpen(true):reset()}><DialogContent className="settings-confirm"><DialogTitle>{secret?'API key created':'New API key'}</DialogTitle>{secret?<><p>This secret is shown once.</p><input className="settings-input" readOnly value={secret} onFocus={event=>event.currentTarget.select()}/><footer><ActionButton onClick={()=>{void navigator.clipboard.writeText(secret);toast.success('Copied')}}>Copy</ActionButton><ActionButton primary onClick={reset}>Done</ActionButton></footer></>:<><label>Name<input className="settings-input" autoFocus value={name} onChange={event=>setName(event.target.value)}/></label><label>Scopes<div className="settings-segmented"><button className={scopes.includes('read')?'active':''} onClick={()=>setScopes(current=>current.includes('read')?current.filter(x=>x!=='read'):[...current,'read'])}>Read</button><button className={scopes.includes('write')?'active':''} onClick={()=>setScopes(current=>current.includes('write')?current.filter(x=>x!=='write'):[...current,'write'])}>Write</button></div></label><fieldset className="settings-check-list"><legend>Team access</legend><label><input type="checkbox" checked={!teamIds.length} onChange={()=>setTeamIds([])}/>All teams</label>{data.teams.map(team=><label key={team.id}><input type="checkbox" checked={teamIds.includes(team.id)} onChange={event=>setTeamIds(current=>event.target.checked?[...current,team.id]:current.filter(id=>id!==team.id))}/>{team.name}</label>)}</fieldset><footer><ActionButton onClick={reset}>Cancel</ActionButton><ActionButton primary disabled={!name.trim()||!scopes.length} onClick={()=>void submit()}>Create key</ActionButton></footer></>}</DialogContent></Dialog>{editingOAuth!==undefined&&<OAuthEditor app={editingOAuth} onClose={()=>setEditingOAuth(undefined)} onSaved={onReload}/>} {editingWebhook!==undefined&&<WebhookEditor data={data} webhook={editingWebhook} onClose={()=>setEditingWebhook(undefined)} onSaved={onReload}/>}</>
}
function ApplicationsPage({data,onReload}:{data:BootstrapData;onReload:()=>Promise<void>}){return <><PageTitle description="Third-party applications authorized for this workspace.">Applications</PageTitle><Section>{data.integrationConnections.map(item=><Row key={item.id} title={item.name} description={`${title(item.provider)} · ${item.status}`}><ActionButton danger onClick={()=>void disconnectIntegration(item.provider).then(onReload)}>Revoke access</ActionButton></Row>)}{!data.integrationConnections.length&&<div className="settings-empty"><AppWindow size={28}/><h3>No authorized applications</h3><p>Applications authorized by workspace members will appear here.</p></div>}</Section></>}
function WebhookEditor({data,webhook,onClose,onSaved}:{data:BootstrapData;webhook:Webhook|null;onClose:()=>void;onSaved:()=>Promise<void>}){const[name,setName]=useState(webhook?.name??'');const[url,setUrl]=useState(webhook?.url??'');const[resourceTypes,setResourceTypes]=useState(webhook?.resourceTypes??['issues']);const[teamIds,setTeamIds]=useState(webhook?.teamIds??[]);const resources=['issues','comments','projects','cycles','documents','customers'];const save=async()=>{try{const input={name:name.trim(),url:url.trim(),resourceTypes,teamIds,enabled:webhook?.enabled??true};if(webhook)await updateWebhook(webhook.id,input);else await createWebhook(input);await onSaved();onClose()}catch(error){toast.error(error instanceof Error?error.message:'Could not save webhook')}};return <Dialog open onOpenChange={value=>!value&&onClose()}><DialogContent className="settings-invite-dialog settings-webhook-dialog"><DialogTitle>{webhook?'Configure webhook':'New webhook'}</DialogTitle><label>Name<input autoFocus value={name} onChange={event=>setName(event.target.value)}/></label><label>Endpoint URL<input value={url} onChange={event=>setUrl(event.target.value)} placeholder="https://example.com/webhooks/flow"/></label><fieldset className="settings-check-list"><legend>Resources</legend>{resources.map(resource=><label key={resource}><input type="checkbox" checked={resourceTypes.includes(resource)} onChange={event=>setResourceTypes(current=>event.target.checked?[...current,resource]:current.filter(item=>item!==resource))}/>{title(resource)}</label>)}</fieldset><fieldset className="settings-check-list"><legend>Teams</legend><label><input type="checkbox" checked={!teamIds.length} onChange={()=>setTeamIds([])}/>All teams</label>{data.teams.map(team=><label key={team.id}><input type="checkbox" checked={teamIds.includes(team.id)} onChange={event=>setTeamIds(current=>event.target.checked?[...current,team.id]:current.filter(id=>id!==team.id))}/>{team.name}</label>)}</fieldset><footer>{webhook&&<ActionButton danger onClick={()=>void deleteWebhook(webhook.id).then(async()=>{await onSaved();onClose()})}>Delete</ActionButton>}<ActionButton onClick={onClose}>Cancel</ActionButton><ActionButton primary disabled={!name.trim()||!/^https?:\/\//.test(url)||!resourceTypes.length} onClick={()=>void save()}>Save</ActionButton></footer></DialogContent></Dialog>}
function OAuthEditor({app,onClose,onSaved}:{app:OAuthApplication|null;onClose:()=>void;onSaved:()=>Promise<void>}){const[name,setName]=useState(app?.name??'');const[description,setDescription]=useState(app?.description??'');const[redirects,setRedirects]=useState(app?.redirectUris.join('\n')??'');const[scopes,setScopes]=useState(app?.scopes.join(', ')??'read');const save=async()=>{try{const input={name,description,redirectUris:redirects.split(/\s+/).filter(Boolean),scopes:scopes.split(',').map(x=>x.trim()).filter(Boolean)};const result=app?await updateOAuthApplication(app.id,input):await createOAuthApplication(input);await onSaved();if(result.clientSecret)toast.success(`Client secret: ${result.clientSecret}`,{duration:15000});onClose()}catch(error){toast.error(error instanceof Error?error.message:'Could not save application')}};return <Dialog open onOpenChange={value=>!value&&onClose()}><DialogContent className="settings-invite-dialog"><DialogTitle>{app?'Configure application':'New application'}</DialogTitle><label>Name<input value={name} onChange={event=>setName(event.target.value)}/></label><label>Description<textarea value={description} onChange={event=>setDescription(event.target.value)}/></label><label>Redirect URIs<textarea value={redirects} onChange={event=>setRedirects(event.target.value)} placeholder="https://app.example.com/oauth/callback"/></label><label>Scopes<input value={scopes} onChange={event=>setScopes(event.target.value)}/></label><footer>{app&&<ActionButton danger onClick={()=>void deleteOAuthApplication(app.id).then(async()=>{await onSaved();onClose()})}>Delete</ActionButton>}<ActionButton onClick={onClose}>Cancel</ActionButton><ActionButton primary disabled={!name.trim()} onClick={()=>void save()}>Save</ActionButton></footer></DialogContent></Dialog>}
function BillingPage({data,onReload}:{data:BootstrapData;onReload:()=>Promise<void>}){const settings=data.workspaceSettings;const[email,setEmail]=useState(settings.billingEmail||data.viewer.email);const save=(patch:Partial<WorkspaceSettings>)=>updateWorkspacePreferences({...settings,...patch}).then(onReload);return <><PageTitle>Billing</PageTitle><Section title="Current plan"><Row title={title(settings.plan)} description={`${data.users.length} members`}><span className="settings-static">Managed locally</span></Row><Row title="Billing email"><input className="settings-input" value={email} onChange={event=>setEmail(event.target.value)} onBlur={()=>email!==settings.billingEmail&&void save({billingEmail:email})}/></Row></Section><Section title="Payments"><div className="settings-empty compact"><CreditCard size={24}/><h3>Billing provider not configured</h3><p>Plan checkout and invoices require a payment provider.</p></div></Section></>}
function UsagePage({ data }: { data: BootstrapData }) {const[usage,setUsage]=useState<Awaited<ReturnType<typeof fetchWorkspaceUsage>>|null>(null);useEffect(()=>{void fetchWorkspaceUsage().then(setUsage)},[data]);if(!usage)return <div className="settings-empty compact"><Gauge size={24}/><p>Loading usage…</p></div>;const save=async(patch:Partial<WorkspaceSettings>)=>{await updateWorkspacePreferences({...data.workspaceSettings,...patch});setUsage(await fetchWorkspaceUsage())};return <><PageTitle description="Current workspace usage, credits, and plan limits.">Usage & limits</PageTitle><Section title="AI credits"><Row title="Credit balance" description="Available workspace credits"><strong className="settings-credit-balance">{currency(usage.aiCredits.balanceCents)}</strong></Row><Row title="Automatic reload" description="Add credits when the balance reaches the threshold"><Toggle label="Automatic credit reload" checked={usage.aiCredits.autoReloadEnabled} onChange={value=>void save({aiCreditAutoReload:value})}/></Row>{usage.aiCredits.autoReloadEnabled&&<><Row title="Reload threshold"><NumberInput value={usage.aiCredits.autoReloadThresholdCents} onCommit={value=>save({aiCreditReloadThresholdCents:value})}/></Row><Row title="Reload amount"><NumberInput value={usage.aiCredits.autoReloadAmountCents} onCommit={value=>save({aiCreditReloadAmountCents:value})}/></Row></>}<Row title="Workspace spend limit" description="Maximum credit spend per billing period"><NumberInput value={usage.aiCredits.workspaceSpendLimitCents} onCommit={value=>save({aiWorkspaceSpendLimitCents:value})}/></Row></Section><Section title="Plan usage"><UsageRow title="Members" value={usage.members} limit={usage.limits.members}/><UsageRow title="Issues" value={usage.issues} limit={usage.limits.issues}/><UsageRow title="File storage (MB)" value={Math.ceil(usage.storageBytes/1048576)} limit={Math.ceil(usage.limits.storageBytes/1048576)}/></Section><Section title="Credit activity">{usage.events.map(event=><Row key={event.id} title={event.feature==='loops'?'Loops':'Coding sessions'} description={`${formatDate(event.createdAt)}${event.userId?` · ${data.users.find(user=>user.id===event.userId)?.displayName??'Member'}`:''}`}><span className="settings-static">{currency(event.amountCents)}</span></Row>)}{!usage.events.length&&<div className="settings-empty compact"><Activity size={24}/><h3>No usage in this period</h3><p>Credit-consuming activity will appear here.</p></div>}</Section></>}
function NumberInput({value,onCommit}:{value:number;onCommit:(value:number)=>void|Promise<void>}){const[draft,setDraft]=useState(String(value/100));useEffect(()=>setDraft(String(value/100)),[value]);return <div className="settings-money-input"><span>$</span><input type="number" min={0} step={1} value={draft} onChange={event=>setDraft(event.target.value)} onBlur={()=>void onCommit(Math.round(Number(draft)*100))}/></div>}
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
function FeaturePage({page,data,onReload}:{page:SettingsPageId;data:BootstrapData;onReload:()=>Promise<void>}){const copy=FEATURE_COPY[page]??{title:page,description:'Workspace feature settings.',rows:[["Enable feature","Make this feature available to workspace members"]] as [string,string][]};if(page==='integrations')return <IntegrationsSettings data={data} onReload={onReload}/>;const enabled=data.workspaceSettings.featureFlags[page]??false;const save=async(value:boolean)=>{try{await updateWorkspacePreferences({...data.workspaceSettings,featureFlags:{...data.workspaceSettings.featureFlags,[page]:value}});await onReload()}catch(error){toast.error(error instanceof Error?error.message:'Could not update feature')}};return <><PageTitle description={copy.description}>{copy.title}</PageTitle><Section><Row title={`Enable ${copy.title}`} description={copy.rows[0]?.[1]}><Toggle label={`Enable ${copy.title}`} checked={enabled} onChange={value=>void save(value)}/></Row></Section></>}
function IntegrationsSettings({data,onReload}:{data:BootstrapData;onReload:()=>Promise<void>}){const update=async(provider:string,configured:boolean)=>{try{if(configured)await disconnectIntegration(provider);else await connectIntegration(provider,{name:title(provider),config:{mode:'workspace-reference'}});await onReload()}catch(error){toast.error(error instanceof Error?error.message:'Could not update integration')}};return <><PageTitle description="Configure provider records. Live OAuth and webhooks require provider credentials.">Integrations</PageTitle><Section>{[['github','Link pull requests and commits to issues'],['slack','Create and update issues from Slack'],['figma','Preview design links in issues']].map(([provider,description])=>{const item=data.integrationConnections.find(connection=>connection.provider===provider);return <Row key={provider} title={title(provider)} description={`${description}. ${item?'Configuration stored':'Provider credentials not configured'}`}><ActionButton danger={Boolean(item)} onClick={()=>void update(provider,Boolean(item))}>{item?'Remove configuration':'Configure reference'}</ActionButton></Row>})}</Section></>}

function storedUserValues(source?:UserSettings):StoredSettings['values']{const values=Object.fromEntries(Object.entries(source??{}).filter(([key])=>!['userId','updatedAt','personalSettingsVersion'].includes(key)));return{...DEFAULT_VALUES,...values} as StoredSettings['values']}
function sameStoredValues(left:StoredSettings['values'],right:StoredSettings['values']){const keys=Object.keys(left);return keys.length===Object.keys(right).length&&keys.every(key=>Object.is(left[key],right[key]))}
function useUserStoredSettings(data:BootstrapData){
  const source=data.userSettings[data.viewer.id]
  const sourceRef=useRef(source)
  sourceRef.current=source
  const dirtyRef=useRef(false)
  const[state,setStateInternal]=useState<StoredSettings>(()=>({values:storedUserValues(source),lists:{}}))
  const setState=useCallback<React.Dispatch<React.SetStateAction<StoredSettings>>>(update=>{dirtyRef.current=true;setStateInternal(update)},[])
  useEffect(()=>{const next=data.userSettings[data.viewer.id];if(next)setStateInternal(current=>{if(dirtyRef.current)return current;const values=storedUserValues(next);return sameStoredValues(current.values,values)?current:{...current,values}})},[data.userSettings,data.viewer.id])
  useEffect(()=>{if(!dirtyRef.current)return;const timeout=window.setTimeout(()=>{const current=sourceRef.current;if(!current)return;dirtyRef.current=false;void updateUserSettings({...current,...state.values} as UserSettings).catch(()=>{dirtyRef.current=true;toast.error('Could not save settings')})},250);return()=>window.clearTimeout(timeout)},[data.viewer.id,state.values])
  useEffect(()=>{const root=document.documentElement;applyTheme(state.values);root.style.fontSize=state.values.fontSize==='Small'?'14px':state.values.fontSize==='Large'?'18px':'';root.classList.toggle('settings-pointer-cursor',Boolean(state.values.pointerCursor));root.classList.toggle('settings-underline-links',Boolean(state.values.underlineLinks))},[state.values])
  return[state,setState]as const
}

function memberCanManage(page:SettingsPageId,settings:WorkspaceSettings){if(['issue-labels','project-labels'].includes(page))return settings.labelPermission==='members';if(['issue-templates','project-templates'].includes(page))return settings.templatePermission==='members';if(page==='api')return settings.apiKeyPermission==='members';return false}
function initials(value: string) { return value.split(/\s+/).filter(Boolean).map(part => part[0]).join("").slice(0,2).toUpperCase() || "W"; }
function title(value: string) { return value ? value[0].toUpperCase() + value.slice(1) : value; }
function formatDate(value: string) { return new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", year: "numeric" }).format(new Date(value)); }
function currency(cents: number) { return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" }).format(cents / 100); }
function slug(value: string) { return value.toLowerCase().replace(/[^a-z0-9-]+/g,"-").replace(/^-+|-+$/g,"").slice(0,32); }
