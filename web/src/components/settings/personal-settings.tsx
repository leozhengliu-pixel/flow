import { useEffect, useMemo, useState, type ReactNode } from "react";
import {
  Bot, CalendarDays, Check, ChevronDown, Code2, ExternalLink, GitFork, Globe,
  KeyRound, Laptop, Mail, MessageCircle, MessageSquare, Monitor, ShieldCheck,
  Smartphone,
} from "lucide-react";
import { toast } from "sonner";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  fetchAccountSessions, removeMember, revokeOtherAccountSessions,
  updateAccountProfile, updateNotificationPreferences,
} from "@/lib/api";
import type { SettingsPageId } from "@/lib/app-routes";
import type { BootstrapData, NotificationPreferences } from "@/types/flow";
import { useI18n } from "@/i18n/i18n";

import "./personal-settings.css";

export type PersonalSettingsValues = Record<string, string | boolean>;

type Props = {
  page: SettingsPageId;
  data: BootstrapData;
  values: PersonalSettingsValues;
  setValue: (key: string, value: string | boolean) => void;
  onNavigate: (page: SettingsPageId) => void;
  onReload: () => Promise<void>;
  onBack: () => void;
};

const EN = {
  preferences: "Preferences", profile: "Profile", notifications: "Notifications",
  codeReviews: "Code & reviews", security: "Security & access", connections: "Connected accounts",
  agents: "Agent personalization", general: "General", interfaceTheme: "Interface and theme",
  desktopApp: "Desktop application", workflows: "Automations and workflows",
  language: "Language", languageDescription: "Choose the language used throughout the application",
};
const ZH = {
  preferences: "偏好设置", profile: "个人资料", notifications: "通知",
  codeReviews: "代码与评审", security: "安全与访问", connections: "已连接账户",
  agents: "Agent 个性化", general: "通用", interfaceTheme: "界面与主题",
  desktopApp: "桌面应用", workflows: "自动化与工作流",
  language: "语言", languageDescription: "选择整个应用使用的语言",
};

export function PersonalSettings(props: Props) {
  const { locale } = useI18n();
  const text = locale === "zh-CN" ? ZH : EN;
  let content: ReactNode;
  if (props.page === "preferences") content = <Preferences {...props} text={text}/>;
  else if (props.page === "profile") content = <Profile {...props} text={text}/>;
  else if (props.page === "notifications") content = <Notifications {...props} text={text}/>;
  else if (props.page === "code-and-reviews") content = <CodeReviews {...props} text={text}/>;
  else if (props.page === "account-security") content = <Security {...props} text={text}/>;
  else if (props.page === "connections") content = <Connections {...props} text={text}/>;
  else content = <Agents {...props} text={text}/>;
  return <div className="personal-settings">{content}</div>;
}

type PersonalProps = Props & { text: typeof EN };

function PageTitle({ children, description }: { children: ReactNode; description?: ReactNode }) {
  return <header className="settings-page-header personal-page-header"><div><h1>{children}</h1>{description && <p>{description}</p>}</div></header>;
}
function Section({ title, description, children }: { title?: string; description?: string; children: ReactNode }) {
  return <section className="settings-section personal-section">{title && <h3>{title}</h3>}{description && <p className="personal-section-description">{description}</p>}<div className="settings-card">{children}</div></section>;
}
function Row({ title, description, icon, children, danger }: { title: ReactNode; description?: ReactNode; icon?: ReactNode; children?: ReactNode; danger?: boolean }) {
  return <div className={`settings-row${danger ? " danger" : ""}`}>{icon && <span className="personal-row-icon">{icon}</span>}<div><strong>{title}</strong>{description && <span>{description}</span>}</div>{children && <div className="settings-control">{children}</div>}</div>;
}
function Toggle({ checked, onChange, label, disabled }: { checked: boolean; onChange: (value: boolean) => void; label: string; disabled?: boolean }) {
  return <button type="button" role="switch" aria-label={label} aria-checked={checked} disabled={disabled} className="personal-toggle" onClick={() => onChange(!checked)}><span/></button>;
}
function Select({ label, value, options, onChange }: { label: string; value: string; options: string[]; onChange: (value: string) => void }) {
  return <DropdownMenu><DropdownMenuTrigger asChild><button className="personal-select" aria-label={label}><span data-i18n-ignore={isBusinessName(value) ? "true" : undefined}>{value}</span><ChevronDown size={12}/></button></DropdownMenuTrigger><DropdownMenuContent align="end" className="personal-select-menu">{options.map(option => <DropdownMenuItem key={option} onSelect={() => onChange(option)}><span data-i18n-ignore={isBusinessName(option) ? "true" : undefined}>{option}</span><Check size={13} className={option === value ? "selected-check" : "hidden-check"}/></DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>;
}
function Action({ children, onClick, danger, primary, disabled, label }: { children: ReactNode; onClick?: () => void; danger?: boolean; primary?: boolean; disabled?: boolean; label?: string }) {
  return <button aria-label={label} disabled={disabled} className={`personal-action${danger ? " danger" : ""}${primary ? " primary" : ""}`} onClick={onClick}>{children}</button>;
}

function Preferences({ values, setValue, text }: PersonalProps) {
  const { locale, setLocale } = useI18n();
  return <><PageTitle>{text.preferences}</PageTitle>
    <Section title={text.general}>
      <Row title={text.language} description={text.languageDescription}><Select label={text.language} value={locale === "zh-CN" ? "简体中文" : "English"} options={["English", "简体中文"]} onChange={value => { const next=value === "简体中文" ? "zh-CN" : "en-US"; setLocale(next); setValue("language", next); }}/></Row>
      <Row title="Default home view" description="Select which view to display when launching Flow"><Select label="Default home view" value={String(values.homeView)} options={["Flow Agent (default)", "Inbox", "My issues"]} onChange={v=>setValue("homeView",v)}/></Row>
      <Row title="Display names" description="Select how names are displayed in the Flow interface"><Select label="Display names" value={String(values.displayNames)} options={["Full name","First name","Username"]} onChange={v=>setValue("displayNames",v)}/></Row>
      <Row title="First day of the week" description="Used for date pickers"><Select label="First day of the week" value={String(values.firstDay)} options={["Monday","Saturday","Sunday"]} onChange={v=>setValue("firstDay",v)}/></Row>
      <Row title="Convert text emoticons into emojis" description="Strings like :) will be converted to 🙂"><Toggle label="Convert text emoticons into emojis" checked={Boolean(values.emoticons)} onChange={v=>setValue("emoticons",v)}/></Row>
      <Row title="Send comments on…" description="Choose which key press is used to submit comments"><Select label="Send comments on…" value={String(values.sendComments)} options={["Enter","⌘ Enter"]} onChange={v=>setValue("sendComments",v)}/></Row>
    </Section>
    <Section title={text.interfaceTheme}>
      <Row title="App sidebar" description="Customize sidebar item visibility, ordering, and badge style"><Action disabled>Customize</Action></Row>
      <Row title="Font size" description="Adjust the size of text across the app"><Select label="Font size" value={String(values.fontSize)} options={["Small","Default","Large"]} onChange={v=>setValue("fontSize",v)}/></Row>
      <Row title="Use pointer cursors" description="Change the cursor to a pointer when hovering over interactive elements"><Toggle label="Use pointer cursors" checked={Boolean(values.pointerCursor)} onChange={v=>setValue("pointerCursor",v)}/></Row>
      <Row title="Underline links" description="Always underline links in text content"><Toggle label="Underline links" checked={Boolean(values.underlineLinks)} onChange={v=>setValue("underlineLinks",v)}/></Row>
    </Section>
    <Section><Row title="Interface theme" description="Select or customize your interface color scheme"><Select label="Interface theme" value={String(values.interfaceTheme)} options={["System preference","Light","Dark"]} onChange={v=>setValue("interfaceTheme",v)}/></Row></Section>
    <Section title={text.desktopApp}><Row title="Open in desktop app" description="Automatically open links in desktop app when possible"><Toggle label="Open in desktop app" checked={Boolean(values.desktopLinks)} onChange={v=>setValue("desktopLinks",v)}/></Row></Section>
    <Section title={text.workflows}>
      <Row title="Auto-assign to self" description="When creating new issues, always assign them to yourself by default"><Toggle label="Auto-assign to self" checked={Boolean(values.autoAssign)} onChange={v=>setValue("autoAssign",v)}/></Row>
      <Row title="On move to started status, assign to yourself" description="When you move an unassigned issue to started, it will be automatically assigned to you"><Toggle label="On move to started status, assign to yourself" checked={Boolean(values.assignStarted)} onChange={v=>setValue("assignStarted",v)}/></Row>
    </Section>
  </>;
}

function Profile({ data, onReload, onBack, text }: PersonalProps) {
  const current=data.userSettings[data.viewer.id];
  const [displayName,setDisplayName]=useState(data.viewer.displayName);
  const [username,setUsername]=useState(current?.username || data.viewer.name);
  const [jobTitle,setJobTitle]=useState(current?.jobTitle || "");
  const [busy,setBusy]=useState(false);
  const [leaveOpen,setLeaveOpen]=useState(false);
  const dirty=displayName !== data.viewer.displayName || username !== (current?.username || data.viewer.name) || jobTitle !== (current?.jobTitle || "");
  const save=async()=>{setBusy(true);try{await updateAccountProfile({displayName,username,jobTitle,avatarUrl:data.viewer.avatarUrl});await onReload();toast.success("Profile updated");}catch(error){toast.error(error instanceof Error?error.message:"Could not update profile");}finally{setBusy(false)}};
  const leave=async()=>{setBusy(true);try{await removeMember(data.workspace.urlKey,data.viewer.id);setLeaveOpen(false);onBack();}catch(error){toast.error(error instanceof Error?error.message:"Could not leave workspace");}finally{setBusy(false)}};
  return <><PageTitle>{text.profile}</PageTitle><Section>
    <Row title="Profile picture"><span className="personal-avatar">{initials(displayName)}</span></Row>
    <Row title="Email"><span className="personal-static">{data.viewer.email}</span></Row>
    <Row title="Full name"><input className="personal-input" aria-label="Full name" value={displayName} onChange={e=>setDisplayName(e.target.value)}/></Row>
    <Row title="Title" description="Your job title or role"><input className="personal-input" aria-label="Title" placeholder="Software engineer" value={jobTitle} onChange={e=>setJobTitle(e.target.value)}/></Row>
    <Row title="Username" description="One word, like a nickname or first name"><input className="personal-input" aria-label="Username" placeholder="username" value={username} onChange={e=>setUsername(e.target.value.replace(/\s+/g,""))}/></Row>
  </Section>{dirty && <div className="personal-save-bar"><Action onClick={()=>{setDisplayName(data.viewer.displayName);setUsername(current?.username||data.viewer.name);setJobTitle(current?.jobTitle||"")}}>Cancel</Action><Action primary disabled={busy||!displayName.trim()||!username.trim()} onClick={()=>void save()}>{busy?"Saving…":"Update"}</Action></div>}
  <Section title="Workspace access"><Row title="Remove yourself from workspace" danger><Action danger onClick={()=>setLeaveOpen(true)}>Leave workspace</Action></Row></Section>
  <Dialog open={leaveOpen} onOpenChange={setLeaveOpen}><DialogContent className="personal-dialog"><DialogTitle>Leave {data.workspace.name}?</DialogTitle><p>You will lose access to this workspace. An administrator must invite you again to restore access.</p><footer><Action onClick={()=>setLeaveOpen(false)}>Cancel</Action><Action danger disabled={busy} onClick={()=>void leave()}>Leave workspace</Action></footer></DialogContent></Dialog>
  </>;
}

function Notifications({ data, values, setValue, onReload, text }: PersonalProps) {
  const initial=useMemo(()=>data.notificationPreferences?.[data.viewer.id] ?? defaultNotificationPreferences(data.viewer.id),[data.notificationPreferences,data.viewer.id]);
  const [preferences,setPreferences]=useState(initial);
  const [channel,setChannel]=useState<"desktop"|"mobile"|"email"|"slack"|null>(null);
  useEffect(()=>setPreferences(initial),[initial]);
  const save=async(next:NotificationPreferences)=>{const before=preferences;setPreferences(next);try{setPreferences(await updateNotificationPreferences(next));await onReload();}catch(error){setPreferences(before);toast.error(error instanceof Error?error.message:"Could not save notifications");}};
  return <><PageTitle>{text.notifications}</PageTitle>
    <Section title="Push notifications" description="Choose which notifications are pushed to your devices. All notifications will still appear in your inbox.">
      <NotificationChannel icon={<Monitor/>} title="Desktop" status={preferences.desktop.enabled?"Enabled":"Disabled"} onClick={()=>setChannel("desktop")}/>
      <NotificationChannel icon={<Smartphone/>} title="Mobile" status="Not available in Flow web" disabled/>
      <NotificationChannel icon={<Mail/>} title="Email" status={preferences.email.enabled?"Enabled for all notifications":"Disabled"} onClick={()=>setChannel("email")}/>
      <NotificationChannel icon={<MessageCircle/>} title="Slack" status="Not connected" disabled/>
    </Section>
    <Section title="Updates from Flow" description="Subscribe to product announcements and important changes from the Flow team">
      <Row title="Show updates in sidebar" description="Highlight new features and improvements in the app sidebar"><Toggle label="Show updates in sidebar" checked={Boolean(values.changelogUpdates)} onChange={v=>setValue("changelogUpdates",v)}/></Row>
      <Row title="Changelog newsletter" description="Receive an email twice a month highlighting new features and improvements"><Toggle label="Changelog newsletter" checked={Boolean(values.changelogNewsletter)} onChange={v=>setValue("changelogNewsletter",v)}/></Row>
    </Section>
    <Section title="Marketing"><Row title="Marketing and onboarding" description="Occasional updates to help you get the most out of Flow"><Toggle label="Marketing and onboarding" checked={Boolean(values.marketingUpdates)} onChange={v=>setValue("marketingUpdates",v)}/></Row></Section>
    <Section title="Other updates">
      <Row title="Invite accepted" description="Email when invitees accept an invite"><Toggle label="Invite accepted" checked={Boolean(values.inviteAcceptedUpdates)} onChange={v=>setValue("inviteAcceptedUpdates",v)}/></Row>
      <Row title="Privacy and legal updates" description="Email when privacy policies or terms of service change"><Toggle label="Privacy and legal updates" checked={Boolean(values.privacyUpdates)} onChange={v=>setValue("privacyUpdates",v)}/></Row>
      <Row title="Data processing agreement (DPA)" description="Email when our DPA changes"><Toggle label="Data processing agreement" checked={Boolean(values.dpaUpdates)} onChange={v=>setValue("dpaUpdates",v)}/></Row>
    </Section>
    <Dialog open={channel!==null} onOpenChange={open=>{if(!open)setChannel(null)}}><DialogContent className="personal-dialog notification-dialog"><DialogTitle>{channel === "email" ? "Email notifications" : "Desktop notifications"}</DialogTitle>{channel && channel !== "mobile" && channel !== "slack" && <><Row title={`Enable ${channel} notifications`}><Toggle label={`Enable ${channel} notifications`} checked={preferences[channel].enabled} onChange={enabled=>void save({...preferences,[channel]:{...preferences[channel],enabled}})}/></Row>{channel === "desktop" && <Row title="Notification sounds"><Toggle label="Notification sounds" checked={preferences.soundEnabled} onChange={soundEnabled=>void save({...preferences,soundEnabled})}/></Row>}<footer><Action onClick={()=>setChannel(null)}>Done</Action></footer></>}</DialogContent></Dialog>
  </>;
}

function NotificationChannel({icon,title,status,onClick,disabled}:{icon:ReactNode;title:string;status:string;onClick?:()=>void;disabled?:boolean}) {
  return <button className="personal-channel" disabled={disabled} onClick={onClick}><span className="personal-channel-icon">{icon}</span><span><strong>{title}</strong><small>{status}</small></span><ChevronDown size={14}/></button>;
}

function CodeReviews({ values, setValue, text }: PersonalProps) {
  return <><PageTitle description="Review GitHub pull requests and agent code diffs in Flow">{text.codeReviews}</PageTitle>
    <Section>
      <Row title="Enable code reviews" description="Review GitHub pull requests, accessible from the sidebar"><Toggle label="Enable code reviews" checked={Boolean(values.codeReviewsEnabled)} onChange={v=>setValue("codeReviewsEnabled",v)}/></Row>
      <Row title="Auto-convert draft pull requests" description="Automatically mark your drafts as ready upon approval or requesting a review"><Toggle label="Auto-convert draft pull requests" checked={Boolean(values.autoConvertDrafts)} onChange={v=>setValue("autoConvertDrafts",v)}/></Row>
      <Row title="Merge strategy" description="Choose the default merge strategy for pull requests"><Select label="Merge strategy" value={String(values.mergeStrategy)} options={["Squash and merge","Merge commit","Rebase and merge"]} onChange={v=>setValue("mergeStrategy",v)}/></Row>
    </Section>
    <Section>
      <Row title="Code theme" description="Select the syntax highlighting theme used in code diffs and viewers"><Select label="Code theme" value={String(values.codeTheme)} options={["Flow Light","Flow Dark","GitHub Light","GitHub Dark"]} onChange={v=>setValue("codeTheme",v)}/></Row>
      <Row title="Font"><Select label="Code font" value={String(values.codeFont)} options={["12px, Regular, Default","13px, Regular, Default","14px, Regular, Default"]} onChange={v=>setValue("codeFont",v)}/></Row>
      <pre className="personal-code-preview"><code><span>const</span> config = {"{"}\n  apiUrl: <i>"https://api.example.com"</i>,\n  timeout: <b>5000</b>,\n  debug: <em>true</em>\n{"}"};</code></pre>
    </Section>
    <Section title="Notifications" description="Choose which review activity appears in your Flow inbox and push notifications">
      <Row title="Comments & reviews" description="Comments, mentions, and submitted reviews"><Select label="Comments & reviews" value={String(values.reviewCommentsFilter)} options={["All","Exclude Bots","Mentions only"]} onChange={v=>setValue("reviewCommentsFilter",v)}/></Row>
      <Row title="Review requests" description="Requests for your personal review"><Toggle label="Review requests" checked={Boolean(values.reviewRequests)} onChange={v=>setValue("reviewRequests",v)}/></Row>
      <Row title="GitHub team review requests" description="Requests for review from your GitHub teams with 10 or fewer members"><Toggle label="GitHub team review requests" checked={Boolean(values.githubTeamReviewRequests)} onChange={v=>setValue("githubTeamReviewRequests",v)}/></Row>
      <Row title="Checks & merge queue" description="Check failures and merge queue updates"><Toggle label="Checks & merge queue" checked={Boolean(values.checksMergeQueue)} onChange={v=>setValue("checksMergeQueue",v)}/></Row>
    </Section>
    <Section title="Signed commits">
      <Row title="Require signed commits" description="Users must upload a signing key before starting a coding session"><Toggle label="Require signed commits" checked={Boolean(values.requireSignedCommits)} onChange={v=>setValue("requireSignedCommits",v)} disabled/></Row>
      <Row title="No signing key added"><Action disabled>Add key</Action></Row>
    </Section>
    <Section title="External tools">
      <Row title="Configure coding tools" description="Configure the external coding tools you can open issues in"><ExternalLink size={16}/></Row>
      <Row title="Git attachment format" description="The format of GitHub/GitLab attachments on issues"><Select label="Git attachment format" value={String(values.gitAttachmentFormat)} options={["Title","URL","Title and URL"]} onChange={v=>setValue("gitAttachmentFormat",v)}/></Row>
      <Row title="On git branch copy, move issue to started status" description="After copying the git branch name, issue status is moved to the team’s first started workflow status."><Toggle label="On git branch copy, move issue to started status" checked={Boolean(values.gitBranchMoveStarted)} onChange={v=>setValue("gitBranchMoveStarted",v)}/></Row>
      <Row title="On open in coding tool, move issue to started status" description="After opening an issue in a coding tool or copying as prompt, issue status is moved to the team’s first started workflow status."><Toggle label="On open in coding tool, move issue to started status" checked={Boolean(values.codingToolMoveStarted)} onChange={v=>setValue("codingToolMoveStarted",v)}/></Row>
    </Section>
  </>;
}

function Security({ data, onNavigate, text }: PersonalProps) {
  const { formatDate } = useI18n();
  const [sessions,setSessions]=useState<Awaited<ReturnType<typeof fetchAccountSessions>>>([]);
  const [confirmOpen,setConfirmOpen]=useState(false);
  useEffect(()=>{void fetchAccountSessions().then(setSessions).catch(error=>toast.error(error instanceof Error?error.message:"Could not load sessions"));},[]);
  const other=sessions.filter(item=>!item.current);
  const revoke=async()=>{try{await revokeOtherAccountSessions();setSessions(await fetchAccountSessions());setConfirmOpen(false);toast.success("Other sessions revoked");}catch(error){toast.error(error instanceof Error?error.message:"Could not revoke sessions");}};
  return <><PageTitle>{text.security}</PageTitle>
    <Section title="Sessions" description="Devices logged into your account">{sessions.filter(item=>item.current).map(item=><Row key={item.id} icon={<Globe/>} title="Current session" description={`Last active ${formatDate(item.lastSeenAt,{dateStyle:"medium",timeStyle:"short"})} · expires ${formatDate(item.expiresAt,{dateStyle:"medium",timeStyle:"short"})}`}/>)}</Section>
    {other.length>0 && <Section title={`${other.length} other session${other.length===1?"":"s"}`}><div className="personal-section-action"><Action danger onClick={()=>setConfirmOpen(true)}>Revoke all</Action></div>{other.map(item=><Row key={item.id} icon={<Laptop/>} title="Signed-in session" description={`Last active ${formatDate(item.lastSeenAt,{dateStyle:"medium",timeStyle:"short"})}`}/>)}</Section>}
    <Section title="Passkeys" description="Passkeys are a secure way to sign in to your Flow account"><div className="personal-empty"><KeyRound/><h3>No passkeys registered</h3><span>Passkey enrollment is not available on this server.</span></div></Section>
    <Section title="Personal API keys" description="Use Flow’s API to build your own integrations"><div className="personal-empty"><Code2/><h3>{data.apiKeys.filter(k=>k.creatorId===data.viewer.id&&!k.revokedAt).length ? `${data.apiKeys.filter(k=>k.creatorId===data.viewer.id&&!k.revokedAt).length} active API key` : "No API keys created"}</h3><Action onClick={()=>onNavigate("api")}>Manage API keys</Action></div></Section>
    <Section title="Commit signing key" description="Coding sessions use this key to sign your commits"><Row title="No signing key added"><Action disabled>Add key</Action></Row></Section>
    <Section title="Authorized applications"><div className="personal-empty"><ShieldCheck/><h3>No authorized applications</h3></div></Section>
    <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}><DialogContent className="personal-dialog"><DialogTitle>Revoke all other sessions?</DialogTitle><p>Every other device will need to sign in again.</p><footer><Action onClick={()=>setConfirmOpen(false)}>Cancel</Action><Action danger onClick={()=>void revoke()}>Revoke all</Action></footer></DialogContent></Dialog>
  </>;
}

function Connections({ data, onNavigate, text }: PersonalProps) {
  const integrations=useMemo(()=>new Map(data.integrationConnections.map(item=>[item.provider,item])),[data.integrationConnections]);
  const entries=[
    {provider:"slack",name:"Slack",description:"Sync your message attribution, and receive notifications in Slack",icon:<MessageCircle/>},
    {provider:"google-calendar",name:"Google Calendar",description:"Sync your calendar out-of-office status to Flow",icon:<CalendarDays/>},
    {provider:"notion",name:"Notion",description:"Preview issues, projects, and views within Notion",icon:<MessageSquare/>},
    {provider:"github",name:"GitHub",description:"Review code in Flow and sync attribution of your git-related actions",icon:<GitFork/>},
  ];
  return <><PageTitle description="Connect your user accounts to sync attribution of your actions between apps">{text.connections}</PageTitle><div className="personal-connection-list">{entries.map(item=>{const connection=integrations.get(item.provider);return <div className="settings-card" key={item.provider}><Row icon={item.icon} title={<span data-i18n-ignore>{item.name}</span>} description={item.description}>{connection?<Action onClick={()=>onNavigate("integrations")}>Connected</Action>:<Action disabled>Unavailable</Action>}</Row></div>})}</div></>;
}

function Agents({ values, setValue, onNavigate, text }: PersonalProps) {
  const [draft,setDraft]=useState(String(values.agentInstructions||""));
  const dirty=draft !== String(values.agentInstructions||"");
  return <><PageTitle description="Your personal settings for Flow Agent">{text.agents}</PageTitle>
    <Section title="Guidance" description="Provide personal instructions and context for Flow Agent when responding to conversations"><div className="personal-agent-editor"><textarea aria-label="AI prompt rules" placeholder="Enter personal guidance for Flow Agent (optional)…" maxLength={4000} value={draft} onChange={e=>setDraft(e.target.value)}/><footer><span>{draft.length}/4000</span><Action primary disabled={!dirty} onClick={()=>setValue("agentInstructions",draft)}>Save</Action></footer></div></Section>
    <Section title="Skills" description="Reusable prompts auto-selected by the agent or invoked via slash commands"><div className="personal-empty"><Bot/><h3>No skills created</h3><span>Personal skill creation is not available in this build.</span></div></Section>
    <Section title="MCP connectors" description="Add MCP connectors for use with Flow Agent. Workspace admins can manage available connectors in security settings."><Row title="Agent MCP access disabled in this workspace"><Action onClick={()=>onNavigate("security")}>Configure</Action></Row></Section>
  </>;
}

function defaultNotificationPreferences(userId:string):NotificationPreferences {
  const categories={assignments:true,statusChanges:true,comments:true,mentions:true,reactions:true,subscriptions:true,documents:true,updates:true,reminders:true,loops:true,integrations:true,billing:true,customerRequests:true,triage:true};
  return {userId,inbox:{enabled:true,categories},email:{enabled:true,categories:{...categories}},desktop:{enabled:false,categories:{...categories}},emailFormat:"digest",delayLowPriority:true,immediateUrgent:true,soundEnabled:true,updatedAt:new Date().toISOString()};
}
function initials(value:string){return value.split(/\s+/).filter(Boolean).slice(0,2).map(part=>part[0]?.toUpperCase()).join("")||"?"}
function isBusinessName(value:string){return /Flow|GitHub|GitLab|Slack|Notion|Google Calendar|MCP/.test(value)}
