import { useEffect,useState } from "react";
import { ArrowLeft, Link2, ExternalLink, Play, RefreshCw, Square, Unplug } from "lucide-react";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { request, jsonRequest } from "@/lib/api-client";
import { disconnectIntegration } from "@/lib/api";
import type { BootstrapData } from "@/types/flow";
import { SettingsSelect } from "./settings-primitives";
import { AgentRichText } from '@/components/agent/agent-rich-text';
import "./connector-dialog.css";

type Result = {
	identifier?:string;
  id?: string;
  issueId?: string;
  title?: string;
  body?: string;
  url?: string;
  status?: string;
  availableUntil?: string;
};
function statusText(value=''){return ({active:'Running',inprogress:'Running',running:'Running',creating:'Starting',finished:'Completed',completed:'Completed',failed:'Failed',error:'Failed',cancelrequested:'Cancel requested',interruptrequested:'Cancel requested',canceled:'Canceled',cancelled:'Canceled',interrupted:'Stopped'} as Record<string,string>)[value.toLowerCase().replace(/[_\s-]/g,'')]??value}
export function ProviderAdapterSettings({
  provider,
  name,
  data,
  onClose,
  onReload,
  embedded=false,
}: {
  provider: string;
  name: string;
  data: BootstrapData;
  onClose: () => void;
  onReload: () => Promise<void>;
  embedded?: boolean;
}) {
  const connection = data.integrationConnections.find(
    (item) => item.provider === provider,
  );
  const [configured, setConfigured] = useState(connection?.status==='connected'||provider==='zapier'&&Boolean(connection));
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<Result>();
  const [resourceID, setResourceID] = useState("");
  const [issueID, setIssueID] = useState("");
  const [teamID, setTeamID] = useState(data.teams[0]?.id ?? "");
  const [repository, setRepository] = useState("");
  const [branch, setBranch] = useState("main");
  const [prompt, setPrompt] = useState("");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [hookURL, setHookURL] = useState("");
  const coding = provider === "cursor" || provider === "codex";
  const [jobs,setJobs]=useState<Result[]>([]);
  useEffect(()=>{if(!coding||!configured)return;let active=true;void request<Result[]>(`/api/integrations/${provider}/jobs`).then(value=>{if(active)setJobs(value)}).catch(()=>{});return()=>{active=false}},[coding,configured,provider]);
  const work = async (fn: () => Promise<void>) => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await fn();
    } catch (reason) {
      setError(
        reason instanceof Error
          ? reason.message
          : "Integration operation failed",
      );
    } finally {
      setBusy(false);
    }
  };
  const connect = () =>
    work(async () => {
      await request(
        `/api/integrations/${provider}/configure`,
        jsonRequest("POST", { name, token, teamId:teamID }),
      );
      setToken("");
      setConfigured(true);
      await onReload();
    });
  const action = (action: string) =>
    work(async () => {
      const saved = await request<Result>(
        `/api/integrations/${provider}/actions`,
        jsonRequest("POST", {
          action,
          resourceId: ["status", "stop"].includes(action)
            ? result?.id
            : resourceID,
          issueId: issueID,
          teamId: teamID,
          repository,
          branch,
          prompt,
          title,
          description,
          url: hookURL,
        }),
      );
      setResult(saved);
      if(coding&&saved.id)setJobs(current=>[saved,...current.filter(item=>item.id!==saved.id)]);
      await onReload();
    });
  const content=(<>
        {!configured ? (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void connect();
            }}
          >
            {provider !== "codex" && provider !== "zapier" ? (
              <label>
                Access token
                <input
                  type="password"
                  autoComplete="new-password"
                  aria-label={`${name} access token`}
                  value={token}
                  onChange={(e) => setToken(e.target.value)}
                  placeholder="Use a token or deployment credentials"
                />
              </label>
            ) : provider === "codex" ? (
              <p>
                Connect the Codex coding environment configured for this
                workspace.
              </p>
            ) : (
              <p>Use a Flow API key with the scopes required by your Zap.</p>
            )}
            <footer>
              <span />
              <button type="button" onClick={onClose}>
                Cancel
              </button>
              <button type="submit" disabled={busy}>
                Verify and connect
              </button>
            </footer>
          </form>
        ) : (
          <div className="provider-adapter-fields">
            <div className="provider-adapter-status">
              <span>
                {connection?.status === "connected"
                  ? "Connected"
                  : "Configured"}
              </span>
              <button
                type="button"
                className="settings-action"
                title="Disconnect integration"
                aria-label="Disconnect integration"
                disabled={busy}
                onClick={() =>
                  void work(async () => {
                    await disconnectIntegration(provider);
                    await onReload();
                    setConfigured(false);
                    setResult(undefined);
                  })
                }
              >
                <Unplug size={14} />
              </button>
            </div>
            {provider === "google-calendar" ? (
              <button
                className="settings-action"
                disabled={busy}
                onClick={() => void action("syncAvailability")}
              >
                <RefreshCw size={14} />
                Sync availability
              </button>
            ) : coding ? (
              <>
                <label>
                Issue identifier
                  <input
                    required
                    value={issueID}
                    onChange={(e) => setIssueID(e.target.value)}
                  placeholder="TEAM-123"
                  />
                </label>
                {provider === "cursor" && (
                  <>
                    <label>
                      Repository
                      <input
                        type="url"
                        value={repository}
                        onChange={(e) => setRepository(e.target.value)}
                        placeholder="https://github.com/owner/repository"
                      />
                    </label>
                    <label>
                      Base branch
                      <input
                        value={branch}
                        onChange={(e) => setBranch(e.target.value)}
                      />
                    </label>
                  </>
                )}
                <label>
                  Instructions
                  <textarea
                    value={prompt}
                    maxLength={8000}
                    onChange={(e) => setPrompt(e.target.value)}
                  />
                </label>
                <div className="provider-adapter-actions">
                  <button
                    className="settings-action"
                    disabled={busy || !issueID}
                    onClick={() => void action("launch")}
                  >
                    <Play size={14} />
                    Start coding
                  </button>
                  {result?.id && (
                    <>
                      <button
                        className="settings-action"
                        disabled={busy}
                        onClick={() => void action("status")}
                      >
                        <RefreshCw size={14} />
                        Refresh status
                      </button>
                      <button
                        className="settings-action"
                        disabled={busy}
                        onClick={() => void action("stop")}
                      >
                        <Square size={14} />
                        Stop
                      </button>
                    </>
                  )}
                </div>
              </>
            ) : provider === "zapier" ? (
              <>
                <label>
                  Run ID
                  <input
                    value={resourceID}
                    onChange={(e) => setResourceID(e.target.value)}
                    placeholder="Unique Zap run ID"
                  />
                </label>
                <label>
                  Title
                  <input
                    value={title}
                    onChange={(e) => setTitle(e.target.value)}
                  />
                </label>
                <label>
                  Description
                  <textarea
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
                  />
                </label>
                <SettingsSelect
                  label="Team"
                  value={teamID}
                  options={data.teams.map((team) => ({
                    value: team.id,
                    label: team.name,
                    entityName: true,
                  }))}
                  onChange={setTeamID}
                />
                <button
                  className="settings-action"
                  disabled={busy || !title || !resourceID}
                  onClick={() => void action("createIssue")}
                >
                  Create issue
                </button>
                <label>
                  Webhook URL
                  <input
                    type="url"
                    value={hookURL}
                    onChange={(e) => setHookURL(e.target.value)}
                  />
                </label>
                <button
                  className="settings-action"
                  disabled={busy || !hookURL}
                  onClick={() => void action("subscribe")}
                >
                  Subscribe to Flow events
                </button>
              </>
            ) : (
              <>
                <label>
                  {provider === "intercom"
                    ? "Conversation ID"
                    : provider === "sentry"
                      ? "Sentry issue ID"
                      : provider === "notion"
                        ? "Page ID"
                        : "File URL or key"}
                  <input
                    value={resourceID}
                    onChange={(e) => setResourceID(e.target.value)}
                    placeholder="Resource URL or ID"
                  />
                </label>
                <button
                  className="settings-action"
                  disabled={busy || !resourceID}
                  onClick={() => void action("preview")}
                >
                  Preview
                </button>
                {["intercom", "sentry"].includes(provider) ? (
                  <>
                    <SettingsSelect
                      label="Destination team"
                      value={teamID}
                      options={data.teams.map((team) => ({
                        value: team.id,
                        label: team.name,
                        entityName: true,
                      }))}
                      onChange={setTeamID}
                    />
                    <button
                      className="settings-action"
                      disabled={busy || !resourceID || !teamID}
                      onClick={() => void action("import")}
                    >
                      Create linked issue
                    </button>
                  </>
                ) : (
                  <>
                    <label>
                    Issue identifier
                      <input
                        value={issueID}
                        onChange={(e) => setIssueID(e.target.value)}
                      />
                    </label>
                    <button
                      className="settings-action"
                      disabled={busy || !resourceID || !issueID}
                      onClick={() => void action("attach")}
                    >
                      Attach to issue
                    </button>
                  </>
                )}
              </>
            )}
          {coding&&jobs.length>0&&<SettingsSelect label="Coding run" value={result?.id??''} options={[{value:'',label:'Select a run'},...jobs.map(job=>({value:job.id!,label:`${job.title||'Coding run'} · ${statusText(job.status)}`}))]} onChange={id=>setResult(jobs.find(job=>job.id===id))}/>}
          {result && (
              <section
                className="provider-adapter-result"
                aria-label="Integration result"
              >
                {result.title && <strong>{result.title}</strong>}
                {result.status && <p role="status">{statusText(result.status)}</p>}
                {result.body && <AgentRichText className="provider-adapter-document" ariaLabel="Integration result content" content={result.body}/>}
                {result.url && (
                  <a
                    href={result.url}
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    Open in {name}
                    <ExternalLink size={14} />
                  </a>
                )}
              {result.issueId && <a href={`/${data.workspace.urlKey}/issue/${encodeURIComponent(result.identifier??result.issueId)}`}>{result.identifier||'Open issue'}<ExternalLink size={14}/></a>}
                {result.availableUntil && (
                  <p>
                    Until {new Date(result.availableUntil).toLocaleString()}
                  </p>
                )}
              </section>
            )}
            <footer>
              <span />
              <button type="button" onClick={onClose} disabled={busy}>
                Close
              </button>
            </footer>
          </div>
        )}
        {error && <p role="alert">{error}</p>}
  </>);
  if(embedded)return <div className="provider-adapter-page"><header className="provider-adapter-header"><button className="provider-adapter-back" type="button" onClick={onClose}><ArrowLeft size={13}/>Integrations</button><Link2 size={36}/><h2 data-i18n-ignore>{name}</h2></header><div className="provider-adapter-body">{content}</div></div>;
  return <Dialog open onOpenChange={value=>{if(!value&&!busy)onClose()}}><DialogContent className="connector-dialog provider-adapter-dialog"><DialogTitle>{name}</DialogTitle>{content}</DialogContent></Dialog>;
}
