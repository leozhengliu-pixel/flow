import { useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { request, jsonRequest } from "@/lib/api-client";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import type { WorkspaceSettings } from "@/types/flow";
import { SettingsSection, SettingsRow } from "./settings-primitives";

type Policy = { id: string; name: string; kind: "mcp" | "oauth"; url?: string; ownerId?: string; shared: boolean; status: string; scopes?: string[] };

export function ApplicationPolicySettings({ admin }: { admin: boolean }) {
  const [items, setItems] = useState<Policy[]>([]);
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [shared, setShared] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const reload = async () => { setItems(await request<Policy[]>("/api/application-policies")); setError(""); };
  useEffect(() => { let active = true; void request<Policy[]>("/api/application-policies").then(value => { if (active) setItems(value); }).catch(reason => { if (active) setError(String(reason)); }); return () => { active = false; }; }, []);
  const save = async (item: Policy) => {
    setBusy(true);
    try { await request("/api/application-policies", jsonRequest("PUT", item)); await reload(); setAdding(false); }
    catch (reason) { toast.error(reason instanceof Error ? reason.message : "Could not save application policy"); }
    finally { setBusy(false); }
  };
  const remove = async (item: Policy) => {
    setBusy(true);
    try { await request(`/api/application-policies/${encodeURIComponent(item.id)}`, { method: "DELETE" }); await reload(); }
    catch (reason) { toast.error(String(reason)); } finally { setBusy(false); }
  };
  return <SettingsSection title="Application approvals and Agent connectors">
    {error && <p role="alert">{error}</p>}
    {items.map(item => <SettingsRow key={item.id} title={item.name} description={`${item.kind === "mcp" ? item.url : item.scopes?.join(", ")} · ${item.status}${item.kind === "mcp" ? item.shared ? " · Workspace" : " · Personal" : ""}`}>
        {admin && item.status !== "approved" && <button className="settings-action" disabled={busy} onClick={() => void save({ ...item, status: "approved" })}>Approve</button>}
        {admin && item.status !== "rejected" && <button className="settings-action" disabled={busy} onClick={() => void save({ ...item, status: "rejected" })}>Reject</button>}
        {(admin||item.kind==='mcp'&&!item.shared)&&<button className="settings-action" aria-label={`Remove ${item.name}`} title={`Remove ${item.name}`} disabled={busy} onClick={() => void remove(item)}><Trash2 size={14} /></button>}
    </SettingsRow>)}
    <button className="settings-action" onClick={() => setAdding(true)}><Plus size={14} />Add MCP connector</button>
    {adding && <Dialog open onOpenChange={value => { if (!value && !busy) setAdding(false); }}><DialogContent className="feature-dialog"><DialogTitle>Add MCP connector</DialogTitle>
      <form onSubmit={event => { event.preventDefault(); void save({ id: "", name, url, kind: "mcp", shared, status: admin ? "approved" : "pending" }); }}>
        <label>Name<input required maxLength={200} value={name} onChange={event => setName(event.target.value)} /></label>
        <label>Server URL<input required type="url" value={url} onChange={event => setUrl(event.target.value)} /></label>
        {admin && <label><input type="checkbox" checked={shared} onChange={event => setShared(event.target.checked)} />Workspace connector</label>}
        <footer className="feature-dialog-footer"><button type="button" className="settings-action" disabled={busy} onClick={() => setAdding(false)}>Cancel</button><button className="settings-action" disabled={busy}>Add connector</button></footer>
      </form>
    </DialogContent></Dialog>}
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
