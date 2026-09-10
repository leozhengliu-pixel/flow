import { useEffect, useState } from "react";
import { Trash2 } from "lucide-react";
import { toast } from "sonner";
import { request, jsonRequest } from "@/lib/api-client";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import type { WorkspaceSettings } from "@/types/flow";
import { SettingsSection, SettingsRow } from "./settings-primitives";
import { ConnectorAuthorization, ConnectorDialog, ConnectorMenu } from './connector-dialog';

type Policy = { id: string; name: string; kind: "mcp" | "oauth"; url?: string; ownerId?: string; shared: boolean; status: string; scopes?: string[] };

export function ApplicationPolicySettings({ admin }: { admin: boolean }) {
  const [items, setItems] = useState<Policy[]>([]);
  const [adding, setAdding] = useState<{name:string;url:string}>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(()=>new URLSearchParams(window.location.search).has('connector_error')?'Connector authorization failed. Try authorizing again.':'');
  const reload = async () => { setItems(await request<Policy[]>("/api/application-policies")); setError(""); };
  useEffect(() => { let active = true; void request<Policy[]>("/api/application-policies").then(value => { if (active) setItems(value); }).catch(reason => { if (active) setError(String(reason)); }); return () => { active = false; }; }, []);
  const save = async (item: Policy) => {
    setBusy(true);
    try { await request("/api/application-policies", jsonRequest("PUT", item)); await reload(); }
    catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not save application policy"); }
    finally { setBusy(false); }
  };
  const remove = async (item: Policy) => {
    setBusy(true);
    try { await request(`/api/application-policies/${encodeURIComponent(item.id)}`, { method: "DELETE" }); await reload(); }
    catch (reason) { toast.error(String(reason)); } finally { setBusy(false); }
  };
  return <SettingsSection className="connector-settings" title={admin?"Application approvals and Agent connectors":"MCP connectors"} description={admin?undefined:"Add MCP connectors for use with Flow Agent. Workspace admins can manage available connectors in security settings."}>
    {error && <p role="alert">{error}</p>}
    {items.map(item => <SettingsRow key={item.id} title={item.name} description={`${item.kind === "mcp" ? item.url : item.scopes?.join(", ")} · ${item.status}${item.kind === "mcp" ? item.shared ? " · Workspace" : " · Personal" : ""}`}>
        {admin && item.status !== "approved" && <button className="settings-action" disabled={busy} onClick={() => void save({ ...item, status: "approved" })}>Approve</button>}
        {admin && item.status !== "rejected" && <button className="settings-action" disabled={busy} onClick={() => void save({ ...item, status: "rejected" })}>Reject</button>}
        {item.kind==='mcp'&&item.status!=='rejected'&&(admin||!item.shared)&&<ConnectorAuthorization id={item.id}/>}
        {(admin||item.kind==='mcp'&&!item.shared)&&<button className="settings-action" aria-label={`Remove ${item.name}`} title={`Remove ${item.name}`} disabled={busy} onClick={() => void remove(item)}><Trash2 size={14} /></button>}
    </SettingsRow>)}
    <div className="connector-empty">{!items.length?<h3>No connectors added</h3>:<span/>}<ConnectorMenu onChoose={(name,url)=>setAdding({name,url})}/></div>
    {adding&&<ConnectorDialog initial={adding} admin={admin} onSaved={reload} onClose={()=>setAdding(undefined)}/>}
  </SettingsSection>;
}

export function DataPrivacyDialog({ kind, settings, onSave, onClose }: { kind: "support" | "health"; settings: WorkspaceSettings; onSave: (patch: Partial<WorkspaceSettings>) => Promise<void>; onClose: () => void }) {
  const [enabled, setEnabled] = useState(kind === "support" ? Boolean(settings.reduceSupportPersonalInfo) : Boolean(settings.hipaaCompliance));
  const [busy, setBusy] = useState(false);
  return <Dialog open onOpenChange={value => { if (!value && !busy) onClose(); }}><DialogContent className="feature-dialog"><DialogTitle>{kind === "support" ? "Support data privacy" : "Sensitive data protection"}</DialogTitle>
    <label><input type="checkbox" checked={enabled} onChange={event => setEnabled(event.target.checked)} />{kind === "support" ? "Do not retain incoming sender identity" : "Restrict external processing"}</label>
    <p>{kind === "support" ? "New email requests omit sender names and addresses from intake records and requesters. Message content and existing records are not redacted." : "Block Agent model calls, MCP connectors and outbound integration deliveries. Email notifications contain a link without business content. These controls do not certify HIPAA compliance."}</p>
    <footer className="feature-dialog-footer"><button className="settings-action" disabled={busy} onClick={onClose}>Cancel</button><button className="settings-action" disabled={busy} onClick={async () => { setBusy(true); try { await onSave(kind === "support" ? { reduceSupportPersonalInfo: enabled } : { hipaaCompliance: enabled }); onClose(); } catch (error) { toast.error(String(error)); } finally { setBusy(false); } }}>Save</button></footer>
  </DialogContent></Dialog>;
}
