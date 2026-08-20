import { useMemo, useRef, useState, type ReactNode } from "react";
import {
  Archive, Bot, Check, ChevronDown, ChevronRight, CircleDot, Code2, FileText,
  Inbox, Mail, MessageSquare, MoreHorizontal, Plus, Radio, Rocket,
  Search, Settings2, Smile, Sparkles, Upload, UsersRound, Zap,
  type LucideIcon,
} from "lucide-react";
import { toast } from "sonner";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { useI18n } from "@/i18n/i18n";
import {
  connectIntegration, createCustomEmoji, createDocumentTemplate, createReleasePipeline,
  deleteDocumentTemplate, disconnectIntegration, updateCustomEmoji, updateDocumentTemplate,
  updateReleasePipeline, updateWorkspacePreferences,
} from "@/lib/api";
import type { SettingsPageId } from "@/lib/app-routes";
import type {
  BootstrapData, CustomEmoji, DocumentTemplate, FeatureOption, FeatureSettings,
  IntegrationConnection, ReleasePipeline, WorkspaceSettings,
} from "@/types/flow";

import "./feature-settings.css";

type FeaturePageId = Extract<SettingsPageId, "ai"|"initiatives"|"documents"|"customer-requests"|"releases"|"pulse"|"asks"|"emojis"|"integrations">;
type Props = { page: FeaturePageId; data: BootstrapData; onReload: () => Promise<void> };

const DEFAULT_FEATURE_SETTINGS: FeatureSettings = {
  aiUsageFeedback: false,
  initiativeUpdateSchedule: "none",
  customerRevenueFormat: "annual",
  customerRevenueCurrency: "USD",
  customerManualEdits: true,
  customerStatuses: [
    { id: "active", name: "Active", color: "#4cb782" },
    { id: "prospect", name: "Prospect", color: "#5e6ad2" },
    { id: "churned", name: "Churned", color: "#f2c94c" },
    { id: "lost", name: "Lost", color: "#eb5757" },
  ],
  customerTiers: [], customerExcludedDomains: [], customerGenericDomains: [],
  pulseWorkspaceSchedule: "daily", asksEmailAddresses: [],
};

export function FeatureSettingsPage({ page, data, onReload }: Props) {
  const [busy, setBusy] = useState(false);
  const settings = useMemo(() => normalizeSettings(data.workspaceSettings), [data.workspaceSettings]);
  const save = async (next: WorkspaceSettings) => {
    setBusy(true);
    try { await updateWorkspacePreferences(next); await onReload(); }
    catch (error) { toast.error(message(error)); }
    finally { setBusy(false); }
  };
  const setEnabled = (id: string, value: boolean) => save({ ...settings, featureFlags: { ...settings.featureFlags, [id]: value } });
  const setFeature = <K extends keyof FeatureSettings>(key: K, value: FeatureSettings[K]) =>
    save({ ...settings, featureSettings: { ...settings.featureSettings, [key]: value } });

  if (page === "ai") return <AIPage settings={settings} busy={busy} setEnabled={setEnabled} setFeature={setFeature}/>;
  if (page === "initiatives") return <InitiativesPage data={data} settings={settings} busy={busy} setEnabled={setEnabled} setFeature={setFeature} onReload={onReload}/>;
  if (page === "documents") return <DocumentsPage data={data} onReload={onReload}/>;
  if (page === "customer-requests") return <CustomerRequestsPage data={data} settings={settings} busy={busy} setEnabled={setEnabled} setFeature={setFeature}/>;
  if (page === "releases") return <ReleasesPage data={data} onReload={onReload}/>;
  if (page === "pulse") return <PulsePage settings={settings} busy={busy} setEnabled={setEnabled} setFeature={setFeature}/>;
  if (page === "asks") return <AsksPage data={data} settings={settings} busy={busy} setFeature={setFeature} onReload={onReload}/>;
  if (page === "emojis") return <EmojisPage data={data} onReload={onReload}/>;
  return <IntegrationsPage data={data} onReload={onReload}/>;
}

function AIPage({settings,busy,setEnabled,setFeature}:{settings:WorkspaceSettings;busy:boolean;setEnabled:(id:string,value:boolean)=>void;setFeature:<K extends keyof FeatureSettings>(key:K,value:FeatureSettings[K])=>void}) {
  const cards = [
    ["ai-agent", "Linear Agent", "Create issues and answer questions about your workspace", Bot],
    ["coding-sessions", "Coding sessions", "Assign or ask Flow to make code changes", Code2],
    ["loops", "Loops", "Automated agent workflows triggered by schedules or issue updates", Radio],
    ["code-intelligence", "Code Intelligence", "Allow agents to analyze and answer questions about your code", Sparkles],
    ["triage-intelligence", "Triage Intelligence", "Infer teams, projects, labels, and assignees", Inbox],
  ] as const;
  return <FeatureShell title="AI & Agents" description="Automate your product development processes and operations with AI">
    <FeatureCard><FeatureRow title="Enable usage feedback" description="Improve AI functionality by sharing usage feedback. Never used to train models"><Toggle checked={settings.featureSettings.aiUsageFeedback} disabled={busy} label="Enable usage feedback" onChange={value=>setFeature("aiUsageFeedback",value)}/></FeatureRow></FeatureCard>
    <FeatureSection title="Linear Agent" description="Create issues and answer questions about your workspace.">
      <FeatureCard>{cards.map(([id,title,description,Icon])=><FeatureRow key={id} icon={Icon} title={title} businessTitle={id==="ai-agent"} description={description} badge={id==="code-intelligence"?"Beta":undefined}><Toggle checked={settings.featureFlags[id]??["ai-agent","coding-sessions","loops"].includes(id)} disabled={busy} label={title} onChange={value=>setEnabled(id,value)}/></FeatureRow>)}</FeatureCard>
    </FeatureSection>
    <FeatureSection title="Installed Agents" description="AI agents can work alongside you as teammates."><FeatureCard><FeatureRow icon={Settings2} title="Installed agents guidance" description="Provide context and instructions for installed agents"><span className="feature-state">Configured in Agent personalization</span></FeatureRow></FeatureCard></FeatureSection>
    <FeatureSection title="AI" description="Control AI assistance throughout Flow"><FeatureCard><FeatureRow icon={MessageSquare} title="Resolved thread summaries" description="Control AI summaries for resolved threads across Flow"><Toggle checked={settings.featureFlags["thread-summaries"]??true} disabled={busy} label="Resolved thread summaries" onChange={value=>setEnabled("thread-summaries",value)}/></FeatureRow></FeatureCard></FeatureSection>
  </FeatureShell>;
}

function InitiativesPage({data,settings,busy,setEnabled,setFeature,onReload}:{data:BootstrapData;settings:WorkspaceSettings;busy:boolean;setEnabled:(id:string,value:boolean)=>void;setFeature:<K extends keyof FeatureSettings>(key:K,value:FeatureSettings[K])=>void;onReload:()=>Promise<void>}) {
  const slack = data.integrationConnections.find(item=>item.provider==="slack");
  const toggleSlack = async()=>{try{if(slack)await disconnectIntegration("slack");else await connectIntegration("slack",{name:"Slack",config:{scope:"initiative-updates"}});await onReload()}catch(error){toast.error(message(error))}};
  return <FeatureShell title="Initiatives" description="Initiatives group multiple projects that contribute toward the same strategic effort. Use initiatives to plan and coordinate larger streams of work and monitor their progress at scale.">
    <FeatureCard><FeatureRow title="Enable Initiatives" description="Visible to all non-guest workspace members"><Toggle checked={settings.featureFlags.initiatives??true} disabled={busy} label="Enable Initiatives" onChange={value=>setEnabled("initiatives",value)}/></FeatureRow></FeatureCard>
    <FeatureSection title="Initiative updates" description="Short status reports about progress and health. Owners receive reminders based on the update schedule.">
      <FeatureCard><FeatureRow title="Update schedule" description="Configure how often updates are expected on initiatives"><FeatureSelect label="Update schedule" value={settings.featureSettings.initiativeUpdateSchedule} options={[{value:"none",label:"No expectation for updates"},{value:"weekly",label:"Weekly"},{value:"biweekly",label:"Every two weeks"},{value:"monthly",label:"Monthly"}]} disabled={busy} onChange={value=>setFeature("initiativeUpdateSchedule",value)}/></FeatureRow>
      <FeatureRow icon={MessageSquare} title="Send initiative updates to a Slack channel" description={slack?"Slack is connected for initiative updates":"Connect a channel to send all initiative updates to"}><FeatureButton onClick={()=>void toggleSlack()}>{slack?"Disconnect":"Connect"}</FeatureButton></FeatureRow></FeatureCard>
    </FeatureSection>
    <FeatureSection title="Labels"><FeatureCard><FeatureRow icon={Zap} title="Initiative labels" description="Manage the labels that can be applied to initiatives in your workspace"><span className="feature-state">{data.labels.filter(label=>label.resourceType==="project").length} labels</span></FeatureRow></FeatureCard></FeatureSection>
  </FeatureShell>;
}

function DocumentsPage({data,onReload}:{data:BootstrapData;onReload:()=>Promise<void>}) {
  const [editing,setEditing]=useState<DocumentTemplate|null|undefined>();
  const templates=data.documentTemplates.filter(item=>!item.teamId||data.teams.some(team=>team.id===item.teamId));
  return <FeatureShell title="Documents"><FeatureSection title="Templates" description="These templates are available when creating documents for any team in the workspace. To create templates that only apply to specific teams, add them as team templates.">
    {templates.length?<FeatureCard>{templates.map(item=><FeatureRow key={item.id} icon={FileText} title={item.name} businessTitle description={item.description||"Document template"}><FeatureButton aria-label={`Edit ${item.name}`} onClick={()=>setEditing(item)}>Edit</FeatureButton></FeatureRow>)}</FeatureCard>:<FeatureEmpty icon={FileText} title="No document templates" action={<FeatureButton primary disabled={!data.teams.length} onClick={()=>setEditing(null)}><Plus size={14}/>New template</FeatureButton>}/>}
    {templates.length>0&&<div className="feature-section-action"><FeatureButton primary disabled={!data.teams.length} onClick={()=>setEditing(null)}><Plus size={14}/>New template</FeatureButton></div>}
    {editing!==undefined&&<DocumentTemplateDialog data={data} template={editing} onClose={()=>setEditing(undefined)} onReload={onReload}/>}
  </FeatureSection></FeatureShell>;
}

function DocumentTemplateDialog({data,template,onClose,onReload}:{data:BootstrapData;template:DocumentTemplate|null;onClose:()=>void;onReload:()=>Promise<void>}) {
  const [name,setName]=useState(template?.name??""); const [content,setContent]=useState(template?.content??""); const [busy,setBusy]=useState(false);
  const save=async()=>{setBusy(true);try{if(template)await updateDocumentTemplate(template.id,{name,content});else await createDocumentTemplate({teamId:data.teams[0].id,name,content});await onReload();onClose()}catch(error){toast.error(message(error))}finally{setBusy(false)}};
  const remove=async()=>{if(!template)return;setBusy(true);try{await deleteDocumentTemplate(template.id);await onReload();onClose()}catch(error){toast.error(message(error))}finally{setBusy(false)}};
  return <FeatureDialog open onClose={onClose} title={template?"Edit document template":"New document template"}><label>Template name<input autoFocus value={name} onChange={event=>setName(event.target.value)}/></label><label>Document template content<textarea placeholder="Click here to start writing…" value={content} onChange={event=>setContent(event.target.value)}/></label><DialogFooter>{template&&<FeatureButton danger disabled={busy} onClick={()=>void remove()}>Delete</FeatureButton>}<span/><FeatureButton disabled={busy} onClick={onClose}>Cancel</FeatureButton><FeatureButton primary disabled={busy||!name.trim()} onClick={()=>void save()}>{template?"Save":"Create"}</FeatureButton></DialogFooter></FeatureDialog>;
}

function CustomerRequestsPage({data,settings,busy,setEnabled,setFeature}:{data:BootstrapData;settings:WorkspaceSettings;busy:boolean;setEnabled:(id:string,value:boolean)=>void;setFeature:<K extends keyof FeatureSettings>(key:K,value:FeatureSettings[K])=>void}) {
  const fs=settings.featureSettings; const [editor,setEditor]=useState<{type:"status"|"tier";item?:FeatureOption}|null>(null); const [domains,setDomains]=useState<"excluded"|"generic"|null>(null);
  const saveOption=(type:"status"|"tier",item:FeatureOption)=>{const key=type==="status"?"customerStatuses":"customerTiers";const list=fs[key];setFeature(key,list.some(value=>value.id===item.id)?list.map(value=>value.id===item.id?item:value):[...list,item])};
  const removeOption=(type:"status"|"tier",id:string)=>{const key=type==="status"?"customerStatuses":"customerTiers";setFeature(key,fs[key].filter(item=>item.id!==id))};
  return <FeatureShell title="Customer requests" description="Associate customers with projects and issues to align development efforts with real user needs. Manage and track customer requests across your entire organization.">
    <FeatureCard><FeatureRow title="Enable Customer requests" description="Workspace-wide access to create and view customer requests"><Toggle checked={settings.featureFlags["customer-requests"]??true} disabled={busy} label="Enable Customer requests" onChange={value=>setEnabled("customer-requests",value)}/></FeatureRow><FeatureRow icon={UsersRound} title="Manage customers" description="Manage your list of customers and their requests"><span className="feature-state">{data.customers.length?`${data.customers.length} customers`:"No customers"}</span></FeatureRow></FeatureCard>
    <FeatureSection title="Issue routing" description="New issues created from a customer page are routed to the default team’s triage or backlog."><FeatureCard><FeatureRow title="Default team for customer requests"><FeatureSelect label="Default team for customer requests" value={fs.customerDefaultTeamId??""} options={[{value:"",label:"No default team"},...data.teams.map(team=>({value:team.id,label:team.name,translate:false}))]} disabled={busy} onChange={value=>setFeature("customerDefaultTeamId",value)}/></FeatureRow></FeatureCard></FeatureSection>
    <FeatureSection title="Customer statuses" description="Define statuses for segmenting customers"><OptionList type="status" items={fs.customerStatuses} onAdd={()=>setEditor({type:"status"})} onEdit={item=>setEditor({type:"status",item})} onRemove={id=>removeOption("status",id)}/></FeatureSection>
    <FeatureSection title="Customer tiers" description="Define tiers for segmenting customers"><OptionList type="tier" items={fs.customerTiers} onAdd={()=>setEditor({type:"tier"})} onEdit={item=>setEditor({type:"tier",item})} onRemove={id=>removeOption("tier",id)}/></FeatureSection>
    <FeatureSection title="Display options"><FeatureCard><FeatureRow title="Revenue formatting" description="Data imports must be annual figures, but can be displayed as monthly or annual"><FeatureSelect label="Revenue formatting" value={fs.customerRevenueFormat} options={[{value:"annual",label:"Annual"},{value:"monthly",label:"Monthly"}]} disabled={busy} onChange={value=>setFeature("customerRevenueFormat",value)}/></FeatureRow><FeatureRow title="Revenue currency" description="The currency used when displaying customer revenue"><FeatureSelect label="Revenue currency" value={fs.customerRevenueCurrency} options={["USD","EUR","GBP","CNY","JPY"].map(value=>({value,label:value}))} disabled={busy} onChange={value=>setFeature("customerRevenueCurrency",value)}/></FeatureRow></FeatureCard></FeatureSection>
    <FeatureSection title="Customer attributes data source" description="Sync customer attributes from an external data source"><FeatureCard><FeatureRow title="External data source"><span className="feature-state">None</span></FeatureRow><FeatureRow title="Enable manual edits" description="Attributes can be edited in the Flow UI"><Toggle checked={fs.customerManualEdits} disabled={busy} label="Enable manual edits" onChange={value=>setFeature("customerManualEdits",value)}/></FeatureRow></FeatureCard></FeatureSection>
    <FeatureSection title="Excluded domains and emails" description="Domains and emails that should never create customer requests"><DomainList values={fs.customerExcludedDomains} empty="No excluded domains and emails" onEdit={()=>setDomains("excluded")}/></FeatureSection>
    <FeatureSection title="Generic domains and emails" description="Domains and emails that are not associated with a specific customer"><DomainList values={fs.customerGenericDomains} empty="No custom generic domains and emails" onEdit={()=>setDomains("generic")}/></FeatureSection>
    {editor&&<OptionDialog {...editor} onClose={()=>setEditor(null)} onSave={item=>{saveOption(editor.type,item);setEditor(null)}}/>}
    {domains&&<DomainDialog title={domains==="excluded"?"Excluded domains and emails":"Generic domains and emails"} values={domains==="excluded"?fs.customerExcludedDomains:fs.customerGenericDomains} onClose={()=>setDomains(null)} onSave={values=>{setFeature(domains==="excluded"?"customerExcludedDomains":"customerGenericDomains",values);setDomains(null)}}/>}
  </FeatureShell>;
}

function ReleasesPage({data,onReload}:{data:BootstrapData;onReload:()=>Promise<void>}) {
  const [query,setQuery]=useState(""); const [showArchived,setShowArchived]=useState(false); const [editing,setEditing]=useState<ReleasePipeline|null|undefined>();
  const pipelines=(data.releasePipelines??[]).filter(item=>showArchived?Boolean(item.archivedAt):!item.archivedAt).filter(item=>item.name.toLowerCase().includes(query.toLowerCase()));
  return <div className="feature-wide"><FeatureShell title="Releases" description="Track which issues ship in each release."><div className="feature-toolbar"><label><Search size={15}/><input type="search" aria-label="Filter by pipeline name" placeholder="Filter by pipeline name…" value={query} onChange={event=>setQuery(event.target.value)}/></label><FeatureSelect label="Pipeline state" value={showArchived?"archived":"active"} options={[{value:"active",label:"Active"},{value:"archived",label:"Archived"}]} onChange={value=>setShowArchived(value==="archived")}/><span/><FeatureButton primary onClick={()=>setEditing(null)}><Plus size={14}/>New pipeline</FeatureButton></div>
    <div className="feature-table"><header><span>Pipeline name</span><span>Teams</span><span>Type</span><span>Releases</span><span/></header>{pipelines.map(item=><button key={item.id} className="feature-table-row" onClick={()=>setEditing(item)}><Rocket size={16}/><strong data-i18n-ignore>{item.name}</strong><span data-i18n-ignore>{item.teamIds.map(id=>data.teams.find(team=>team.id===id)?.name).filter(Boolean).join(", ")||"All teams"}</span><span>{item.type==="scheduled"?"Scheduled":"Continuous"}</span><span>{data.releases.length}</span><ChevronRight size={15}/></button>)}{!pipelines.length&&<FeatureEmpty icon={Rocket} title={query?"No matching pipelines":showArchived?"No archived pipelines":"No release pipelines"}/>}</div>
    {editing!==undefined&&<ReleasePipelineDialog data={data} pipeline={editing} onClose={()=>setEditing(undefined)} onReload={onReload}/>}</FeatureShell></div>;
}

function ReleasePipelineDialog({data,pipeline,onClose,onReload}:{data:BootstrapData;pipeline:ReleasePipeline|null;onClose:()=>void;onReload:()=>Promise<void>}) {
  const [name,setName]=useState(pipeline?.name??""); const [teamIds,setTeamIds]=useState(pipeline?.teamIds??[]); const [type,setType]=useState<ReleasePipeline["type"]>(pipeline?.type??"scheduled"); const [production,setProduction]=useState(pipeline?.production??true); const [stages,setStages]=useState((pipeline?.stages??["Planned","In Progress","Released","Canceled"]).join("\n")); const [busy,setBusy]=useState(false);
  const save=async()=>{setBusy(true);try{const input={name,teamIds,type,production,stages:stages.split("\n").map(value=>value.trim()).filter(Boolean)};if(pipeline)await updateReleasePipeline(pipeline.id,input);else await createReleasePipeline(input);await onReload();onClose()}catch(error){toast.error(message(error))}finally{setBusy(false)}};
  const archive=async()=>{if(!pipeline)return;setBusy(true);try{await updateReleasePipeline(pipeline.id,{archived:!pipeline.archivedAt});await onReload();onClose()}catch(error){toast.error(message(error))}finally{setBusy(false)}};
  return <FeatureDialog open onClose={onClose} title={pipeline?"Configure release pipeline":"Create a new release pipeline"}><label>Name<input autoFocus value={name} onChange={event=>setName(event.target.value)}/></label><fieldset><legend>Teams</legend>{data.teams.map(team=><label className="feature-check" key={team.id}><input type="checkbox" checked={teamIds.includes(team.id)} onChange={event=>setTeamIds(current=>event.target.checked?[...current,team.id]:current.filter(id=>id!==team.id))}/>{team.name}</label>)}</fieldset><label>Type<FeatureSelect label="Pipeline type" value={type} options={[{value:"scheduled",label:"Scheduled"},{value:"continuous",label:"Continuous"}]} onChange={value=>setType(value as ReleasePipeline["type"])}/></label><label className="feature-inline-check"><input type="checkbox" checked={production} onChange={event=>setProduction(event.target.checked)}/>Production</label><label>Stages<textarea value={stages} onChange={event=>setStages(event.target.value)}/></label><DialogFooter>{pipeline&&<FeatureButton danger disabled={busy} onClick={()=>void archive()}><Archive size={14}/>{pipeline.archivedAt?"Restore":"Archive"}</FeatureButton>}<span/><FeatureButton onClick={onClose}>Cancel</FeatureButton><FeatureButton primary disabled={busy||!name.trim()||!stages.trim()} onClick={()=>void save()}>{pipeline?"Save":"Create pipeline"}</FeatureButton></DialogFooter></FeatureDialog>;
}

function PulsePage({settings,busy,setEnabled,setFeature}:{settings:WorkspaceSettings;busy:boolean;setEnabled:(id:string,value:boolean)=>void;setFeature:<K extends keyof FeatureSettings>(key:K,value:FeatureSettings[K])=>void}) {
  return <FeatureShell title="Pulse" description="Pulse centralizes all your project and initiative updates into a single feed. Members can choose to receive summary notifications daily or weekly."><FeatureCard><FeatureRow title="Enable Pulse" description="Workspace-wide feed of updates with optional summary notifications"><Toggle checked={settings.featureFlags.pulse??true} disabled={busy} label="Enable Pulse" onChange={value=>setEnabled("pulse",value)}/></FeatureRow></FeatureCard><FeatureSection title="Summary notifications" description="Pulse summary notifications can be delivered in the mornings based on a set schedule"><FeatureCard><FeatureRow title="Default workspace schedule" description="Applies to all members who haven’t set their own preference"><FeatureSelect label="Default workspace schedule" value={settings.featureSettings.pulseWorkspaceSchedule} options={[{value:"daily",label:"Daily"},{value:"weekly",label:"Weekly"},{value:"never",label:"Never"}]} disabled={busy} onChange={value=>setFeature("pulseWorkspaceSchedule",value)}/></FeatureRow></FeatureCard></FeatureSection></FeatureShell>;
}

function AsksPage({data,settings,busy,setFeature,onReload}:{data:BootstrapData;settings:WorkspaceSettings;busy:boolean;setFeature:<K extends keyof FeatureSettings>(key:K,value:FeatureSettings[K])=>void;onReload:()=>Promise<void>}) {
  const [emailOpen,setEmailOpen]=useState(false); const slack=data.integrationConnections.find(item=>item.provider==="slack");
  const toggleSlack=async()=>{try{if(slack)await disconnectIntegration("slack");else await connectIntegration("slack",{name:"Slack",config:{scope:"asks"}});await onReload()}catch(error){toast.error(message(error))}};
  return <FeatureShell title="Asks" description="Let anyone submit bug reports, feature requests, and more using structured templates from Slack or email."><FeatureSection title="Slack" description="Allow anyone in your Slack workspace to submit Asks using templated forms">{slack?<FeatureCard><FeatureRow icon={MessageSquare} title={slack.name} businessTitle description="Connected workspace"><FeatureButton danger onClick={()=>void toggleSlack()}>Disconnect</FeatureButton></FeatureRow></FeatureCard>:<FeatureEmpty icon={MessageSquare} title="No workspaces connected" action={<FeatureButton onClick={()=>void toggleSlack()}><Plus size={14}/>Connect workspace</FeatureButton>}/>}</FeatureSection><FeatureSection title="Email" description="Allow anyone to submit Asks by emailing a custom address">{settings.featureSettings.asksEmailAddresses.length?<FeatureCard>{settings.featureSettings.asksEmailAddresses.map(email=><FeatureRow key={email} icon={Mail} title={email} businessTitle><FeatureButton danger disabled={busy} onClick={()=>setFeature("asksEmailAddresses",settings.featureSettings.asksEmailAddresses.filter(value=>value!==email))}>Remove</FeatureButton></FeatureRow>)}</FeatureCard>:<FeatureEmpty icon={Mail} title="No email addresses configured" action={<FeatureButton onClick={()=>setEmailOpen(true)}><Plus size={14}/>Add email</FeatureButton>}/>}<div className="feature-section-action">{settings.featureSettings.asksEmailAddresses.length>0&&<FeatureButton onClick={()=>setEmailOpen(true)}><Plus size={14}/>Add email</FeatureButton>}</div></FeatureSection>{emailOpen&&<EmailDialog onClose={()=>setEmailOpen(false)} onSave={email=>{setFeature("asksEmailAddresses",[...new Set([...settings.featureSettings.asksEmailAddresses,email])]);setEmailOpen(false)}}/>}</FeatureShell>;
}

function EmojisPage({data,onReload}:{data:BootstrapData;onReload:()=>Promise<void>}) {
  const [query,setQuery]=useState(""); const [showArchived,setShowArchived]=useState(false); const [upload,setUpload]=useState<{name:string;imageUrl:string}|null>(null); const fileRef=useRef<HTMLInputElement>(null);
  const emojis=(data.customEmojis??[]).filter(item=>showArchived?Boolean(item.archivedAt):!item.archivedAt).filter(item=>item.name.includes(query.toLowerCase()));
  const choose=(file?:File)=>{if(!file)return;if(file.size>500_000){toast.error("Emoji images must be smaller than 500 KB");return}const reader=new FileReader();reader.onload=()=>setUpload({name:file.name.replace(/\.[^.]+$/,"").toLowerCase().replace(/[^a-z0-9_-]+/g,"-"),imageUrl:String(reader.result)});reader.readAsDataURL(file)};
  const archive=async(item:CustomEmoji)=>{try{await updateCustomEmoji(item.id,{archived:!item.archivedAt});await onReload()}catch(error){toast.error(message(error))}};
  return <div className="feature-wide"><FeatureShell title="Emojis"><div className="feature-toolbar"><label><Search size={15}/><input type="search" aria-label="Filter by name" placeholder="Filter by name…" value={query} onChange={event=>setQuery(event.target.value)}/></label><FeatureSelect label="Emoji state" value={showArchived?"archived":"active"} options={[{value:"active",label:"Active"},{value:"archived",label:"Archived"}]} onChange={value=>setShowArchived(value==="archived")}/><span/><input ref={fileRef} className="feature-file" type="file" accept="image/png,image/jpeg,image/gif,image/webp" onChange={event=>choose(event.target.files?.[0])}/><FeatureButton onClick={()=>fileRef.current?.click()}><Upload size={14}/>Upload</FeatureButton></div>{emojis.length?<div className="feature-emoji-grid">{emojis.map(item=><div key={item.id}><img src={item.imageUrl} alt=""/><strong data-i18n-ignore>:{item.name}:</strong><span>by <b data-i18n-ignore>{item.creator.displayName}</b></span><FeatureButton aria-label={`${item.archivedAt?"Restore":"Archive"} ${item.name}`} onClick={()=>void archive(item)}>{item.archivedAt?"Restore":"Archive"}</FeatureButton></div>)}</div>:<FeatureEmpty icon={Smile} title={showArchived?"No archived emojis":"No emojis"}/>} {upload&&<EmojiDialog input={upload} onClose={()=>setUpload(null)} onReload={onReload}/>}</FeatureShell></div>;
}

const INTEGRATIONS: {provider:string;name:string;description:string;category:string;icon:LucideIcon}[] = [
  {provider:"github",name:"GitHub",description:"Automate pull request workflows and link code to issues",category:"Essentials",icon:Code2},
  {provider:"slack",name:"Slack",description:"Create issues from Slack messages and sync threads",category:"Essentials",icon:MessageSquare},
  {provider:"gitlab",name:"GitLab",description:"Automate your merge request workflow",category:"Engineering",icon:Code2},
  {provider:"figma",name:"Figma",description:"Create and link issues directly from Figma",category:"Essentials",icon:FileText},
  {provider:"intercom",name:"Intercom",description:"Keep a tight feedback loop with customers",category:"Customer support",icon:MessageSquare},
  {provider:"codex",name:"Codex",description:"Delegate issues to Codex directly from Flow",category:"Agents",icon:Bot},
  {provider:"cursor",name:"Cursor",description:"Turn issues into pull requests with Cursor agents",category:"Agents",icon:Sparkles},
  {provider:"sentry",name:"Sentry",description:"Create and link issues from application errors",category:"Engineering",icon:CircleDot},
  {provider:"zapier",name:"Zapier",description:"Build custom automations to create or update issues",category:"Automation",icon:Zap},
];

function IntegrationsPage({data,onReload}:{data:BootstrapData;onReload:()=>Promise<void>}) {
  const {t}=useI18n();
  const [query,setQuery]=useState(""); const [category,setCategory]=useState("All"); const [busy,setBusy]=useState("");
  const list=INTEGRATIONS.filter(item=>(category==="All"||item.category===category)&&`${item.name} ${item.description}`.toLowerCase().includes(query.toLowerCase()));
  const toggle=async(item:typeof INTEGRATIONS[number],connection?:IntegrationConnection)=>{setBusy(item.provider);try{if(connection)await disconnectIntegration(item.provider);else await connectIntegration(item.provider,{name:item.name,config:{mode:"workspace"}});await onReload()}catch(error){toast.error(message(error))}finally{setBusy("")}};
  return <div className="feature-integrations"><FeatureShell title="Integrations" description="Enhance your Flow experience with a wide variety of add-ons and integrations"><div className="feature-integration-search"><Search size={16}/><input aria-label={t("Search integrations")} placeholder={t("Search integrations")} value={query} onChange={event=>setQuery(event.target.value)}/></div><div className="feature-categories" role="tablist" aria-label={t("Integration categories")}>{["All","Essentials","Agents","Engineering","Customer support","Automation"].map(value=><button role="tab" aria-selected={category===value} key={value} onClick={()=>setCategory(value)}>{t(value)}</button>)}</div><div className="feature-integration-grid">{list.map(item=>{const connection=data.integrationConnections.find(value=>value.provider===item.provider);const Icon=item.icon;return <article key={item.provider}><Icon size={25}/><div><h3><span data-i18n-ignore>{item.name}</span>{connection&&<small>{t("Enabled")}</small>}</h3><p>{t(item.description)}</p></div><FeatureButton primary={!connection} danger={Boolean(connection)} disabled={busy===item.provider} onClick={()=>void toggle(item,connection)}>{t(connection?"Disconnect":"Connect")}</FeatureButton></article>})}</div>{!list.length&&<FeatureEmpty icon={Search} title="No integrations found"/>}</FeatureShell></div>;
}

function FeatureShell({title,description,children}:{title:string;description?:string;children:ReactNode}) { const {t}=useI18n();return <div className="feature-settings"><header className="feature-header"><h1>{title}</h1>{description&&<p>{t(description)}</p>}</header>{children}</div> }
function FeatureSection({title,description,children}:{title:string;description?:string;children:ReactNode}) { const {t}=useI18n();return <section className="feature-section"><header><h2>{t(title)}</h2>{description&&<p>{t(description)}</p>}</header>{children}</section> }
function FeatureCard({children}:{children:ReactNode}) {return <div className="feature-card">{children}</div>}
function FeatureRow({title,description,icon:Icon,badge,businessTitle,children}:{title:string;description?:string;icon?:LucideIcon;badge?:string;businessTitle?:boolean;children?:ReactNode}) { const {t}=useI18n();return <div className="feature-row">{Icon&&<span className="feature-row-icon"><Icon size={18}/></span>}<div><strong data-i18n-ignore={businessTitle||undefined}>{businessTitle?title:t(title)}{badge&&<small>{badge}</small>}</strong>{description&&<span>{t(description)}</span>}</div>{children&&<aside>{children}</aside>}</div> }
function FeatureButton({children,primary,danger,...props}:React.ButtonHTMLAttributes<HTMLButtonElement>&{primary?:boolean;danger?:boolean}) {return <button {...props} className={`feature-button${primary?" primary":""}${danger?" danger":""}`}>{children}</button>}
function Toggle({checked,onChange,label,disabled}:{checked:boolean;onChange:(value:boolean)=>void;label:string;disabled?:boolean}) {return <button type="button" role="switch" aria-label={label} aria-checked={checked} disabled={disabled} className="feature-toggle" onClick={()=>onChange(!checked)}><span/></button>}
function FeatureSelect({label,value,options,onChange,disabled}:{label:string;value:string;options:{value:string;label:string;translate?:boolean}[];onChange:(value:string)=>void;disabled?:boolean}) {const {t}=useI18n();const selected=options.find(item=>item.value===value)??{value,label:value};const text=(item:{label:string;translate?:boolean})=>item.translate===false?item.label:t(item.label);return <DropdownMenu><DropdownMenuTrigger asChild><button type="button" role="combobox" aria-label={t(label)} disabled={disabled} className="feature-select"><span data-i18n-ignore={selected.translate===false?true:undefined}>{text(selected)}</span><ChevronDown size={13}/></button></DropdownMenuTrigger><DropdownMenuContent align="end" className="feature-select-menu">{options.map(item=><DropdownMenuItem key={item.value} data-i18n-ignore={item.translate===false?true:undefined} onSelect={()=>onChange(item.value)}>{text(item)}{item.value===value&&<Check size={13}/>}</DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>}
function FeatureEmpty({icon:Icon,title,action}:{icon:LucideIcon;title:string;action?:ReactNode}) {const {t}=useI18n();return <div className="feature-empty"><Icon size={24}/><h3>{t(title)}</h3>{action}</div>}
function FeatureDialog({open,onClose,title,children}:{open:boolean;onClose:()=>void;title:string;children:ReactNode}) {const {t}=useI18n();return <Dialog open={open} onOpenChange={value=>!value&&onClose()}><DialogContent className="feature-dialog"><DialogTitle>{t(title)}</DialogTitle>{children}</DialogContent></Dialog>}
function DialogFooter({children}:{children:ReactNode}) {return <footer className="feature-dialog-footer">{children}</footer>}

function OptionList({type,items,onAdd,onEdit,onRemove}:{type:"status"|"tier";items:FeatureOption[];onAdd:()=>void;onEdit:(item:FeatureOption)=>void;onRemove:(id:string)=>void}) {const {t}=useI18n();const countLabel=items.length?`${items.length} ${t(type==="status"?"customer statuses":"customer tiers")}`:t(type==="status"?"No customer statuses":"No customer tiers");return <FeatureCard><div className="feature-list-title"><span>{countLabel}</span><FeatureButton aria-label={t(type==="status"?"Create new customer status":"Create new customer tier")} onClick={onAdd}><Plus size={14}/></FeatureButton></div>{items.map(item=><div className="feature-option-row" key={item.id}><i style={{background:item.color}}/><strong data-i18n-ignore>{item.name}</strong><DropdownMenu><DropdownMenuTrigger asChild><button aria-label={`${t("Open menu")}: ${item.name}`}><MoreHorizontal size={15}/></button></DropdownMenuTrigger><DropdownMenuContent align="end" className="feature-select-menu"><DropdownMenuItem onSelect={()=>onEdit(item)}>{t("Edit")}</DropdownMenuItem><DropdownMenuItem className="danger-item" onSelect={()=>onRemove(item.id)}>{t("Delete")}</DropdownMenuItem></DropdownMenuContent></DropdownMenu></div>)}</FeatureCard>}
function OptionDialog({type,item,onClose,onSave}:{type:"status"|"tier";item?:FeatureOption;onClose:()=>void;onSave:(item:FeatureOption)=>void}) {const [name,setName]=useState(item?.name??"");const [color,setColor]=useState(item?.color??"#5e6ad2");const title=item?(type==="status"?"Edit customer status":"Edit customer tier"):(type==="status"?"New customer status":"New customer tier");return <FeatureDialog open onClose={onClose} title={title}><label>Name<input autoFocus value={name} onChange={event=>setName(event.target.value)}/></label><label>Color<input className="feature-color" type="color" value={color} onChange={event=>setColor(event.target.value)}/></label><DialogFooter><span/><FeatureButton onClick={onClose}>Cancel</FeatureButton><FeatureButton primary disabled={!name.trim()} onClick={()=>onSave({id:item?.id??`${type}-${Date.now()}`,name:name.trim(),color})}>Save</FeatureButton></DialogFooter></FeatureDialog>}
function DomainList({values,empty,onEdit}:{values:string[];empty:string;onEdit:()=>void}) {return <FeatureCard><div className="feature-domain-row"><strong>{values.length?values.join(", "):empty}</strong><FeatureButton aria-label="Open menu" onClick={onEdit}>{values.length?"Edit":<Plus size={14}/>}</FeatureButton></div></FeatureCard>}
function DomainDialog({title,values,onClose,onSave}:{title:string;values:string[];onClose:()=>void;onSave:(values:string[])=>void}) {const [text,setText]=useState(values.join("\n"));return <FeatureDialog open onClose={onClose} title={title}><label>One domain or email per line<textarea autoFocus value={text} onChange={event=>setText(event.target.value)}/></label><DialogFooter><span/><FeatureButton onClick={onClose}>Cancel</FeatureButton><FeatureButton primary onClick={()=>onSave([...new Set(text.split(/[\n,]+/).map(value=>value.trim().toLowerCase()).filter(Boolean))])}>Save</FeatureButton></DialogFooter></FeatureDialog>}
function EmailDialog({onClose,onSave}:{onClose:()=>void;onSave:(email:string)=>void}) {const [email,setEmail]=useState("");const valid=/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email);return <FeatureDialog open onClose={onClose} title="Add Ask email"><label>Email address<input autoFocus type="email" value={email} onChange={event=>setEmail(event.target.value)}/></label><DialogFooter><span/><FeatureButton onClick={onClose}>Cancel</FeatureButton><FeatureButton primary disabled={!valid} onClick={()=>onSave(email.toLowerCase())}>Add email</FeatureButton></DialogFooter></FeatureDialog>}
function EmojiDialog({input,onClose,onReload}:{input:{name:string;imageUrl:string};onClose:()=>void;onReload:()=>Promise<void>}) {const [name,setName]=useState(input.name);const [busy,setBusy]=useState(false);const save=async()=>{setBusy(true);try{await createCustomEmoji({name,imageUrl:input.imageUrl});await onReload();onClose()}catch(error){toast.error(message(error))}finally{setBusy(false)}};return <FeatureDialog open onClose={onClose} title="Upload emoji"><div className="feature-emoji-preview"><img src={input.imageUrl} alt="Preview"/></div><label>Name<input autoFocus value={name} onChange={event=>setName(event.target.value)}/></label><DialogFooter><span/><FeatureButton onClick={onClose}>Cancel</FeatureButton><FeatureButton primary disabled={busy||!name.trim()} onClick={()=>void save()}>Upload</FeatureButton></DialogFooter></FeatureDialog>}

function normalizeSettings(settings:WorkspaceSettings):WorkspaceSettings {return {...settings,featureFlags:settings.featureFlags??{},featureSettings:{...DEFAULT_FEATURE_SETTINGS,...(settings.featureSettings??{}),customerStatuses:settings.featureSettings?.customerStatuses?.length?settings.featureSettings.customerStatuses:DEFAULT_FEATURE_SETTINGS.customerStatuses,customerTiers:settings.featureSettings?.customerTiers??[],customerExcludedDomains:settings.featureSettings?.customerExcludedDomains??[],customerGenericDomains:settings.featureSettings?.customerGenericDomains??[],asksEmailAddresses:settings.featureSettings?.asksEmailAddresses??[]}}}
function message(error:unknown){return error instanceof Error?error.message:"Could not update feature settings"}
