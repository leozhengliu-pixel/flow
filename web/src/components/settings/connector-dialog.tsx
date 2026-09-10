import { useEffect, useState } from "react";
import { ChevronRight, Link2, Plus, Trash2 } from "lucide-react";
import * as Popover from "@radix-ui/react-popover";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { usePropertyCommand } from "@/components/property/use-property-command";
import { jsonRequest, request } from "@/lib/api-client";
import "./connector-dialog.css";

type ConnectorEntry={id:string;label:string;url:string;keywords:string;children?:ConnectorEntry[]};
const regions=(name:string,values:[string,string][]):ConnectorEntry=>({id:name,label:name,url:'',keywords:name,children:values.map(([label,url])=>({id:url,label,url,keywords:label}))});
const CONNECTOR_CATALOG:ConnectorEntry[] = [
  ["Attio", "https://mcp.attio.com/mcp"],
  ["BetterStack", "https://mcp.betterstack.com"],
  ["GitHub", "https://api.githubcopilot.com/mcp"],
  ["Granola", "https://mcp.granola.ai/mcp"],
  ["HubSpot", "https://mcp.hubspot.com/"],
  ["incident.io", "https://mcp.incident.io/mcp"],
  ["Intercom", "https://mcp.intercom.com/mcp"],
  ["Jam", "https://mcp.jam.dev/mcp"],
  ["Notion", "https://mcp.notion.com/mcp"],
  ["PostHog", "https://mcp.posthog.com/mcp"],
  ["Sanity", "https://mcp.sanity.io"],
  ["Sentry", "https://mcp.sentry.dev/mcp"],
  ["Slack Personal", "https://mcp.slack.com/mcp"],
  ["Stripe", "https://mcp.stripe.com"],
  ["Zapier", "https://mcp.zapier.com/api/v1/connect"],
].map(([name, url]) => ({ id: url, label: name, url, keywords: url }));
CONNECTOR_CATALOG.push(regions('Amplitude',[['US','https://mcp.amplitude.com/mcp'],['EU','https://mcp.eu.amplitude.com/mcp']]),regions('Datadog',[['US1 - East','https://mcp.datadoghq.com/api/unstable/mcp-server/mcp'],['US3 - West','https://mcp.us3.datadoghq.com/api/unstable/mcp-server/mcp'],['US5 - Central','https://mcp.us5.datadoghq.com/api/unstable/mcp-server/mcp'],['EU1 - Europe','https://mcp.datadoghq.eu/api/unstable/mcp-server/mcp'],['AP1 - Japan','https://mcp.ap1.datadoghq.com/api/unstable/mcp-server/mcp'],['AP2 - Australia','https://mcp.ap2.datadoghq.com/api/unstable/mcp-server/mcp']]),{id:'glean',label:'Glean',url:'',keywords:'Glean'});
CONNECTOR_CATALOG.sort((a,b)=>a.label.localeCompare(b.label));

export function ConnectorMenu({
  onChoose,
}: {
  onChoose: (name: string, url: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [region,setRegion]=useState<string>();
  const [focusRegion,setFocusRegion]=useState(false);
  const options:ConnectorEntry[] = [
    ...CONNECTOR_CATALOG,
    { id: "custom", label: "Custom URL…", url: "", keywords: "" },
  ];
  const command = usePropertyCommand({
    open,
    options,
    closeOnSelect:false,
    onOpenChange: setOpen,
    onSelect: (item) => {if(item.children){setFocusRegion(true);setRegion(item.id);return}setOpen(false);onChoose(item.id==='custom'?'':item.label,item.url)},
  });
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <button type="button" className="settings-action">
          <Plus size={14} />
          Add connector
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          className="connector-menu"
          data-flow-motion="floating"
          sideOffset={4}
          align="end"
          aria-label="Connectors"
          onKeyDown={command.onKeyDown}
          onOpenAutoFocus={(e) => {
            e.preventDefault();
            requestAnimationFrame(() => command.inputRef.current?.focus());
          }}
        >
          <input
            ref={command.inputRef}
            type="search"
            aria-label="Search connectors"
            placeholder="Search connectors…"
            value={command.query}
            onChange={(e) => {command.onQueryChange(e.target.value);const exact=options.find(item=>item.label.toLowerCase().includes(e.target.value.toLowerCase()));if(exact)command.setActiveId(exact.id)}}
          />
          <div role="listbox" aria-label="MCP connectors">
            <small>MCPs</small>
          {command.filteredOptions.map((item) => <Popover.Root key={item.id} open={region===item.id&&Boolean(item.children)} onOpenChange={next=>setRegion(next?item.id:undefined)}><Popover.Anchor asChild>
            <button
                type="button"
                key={item.id}
                role="option"
                aria-selected={command.activeId === item.id}
              onPointerMove={() => command.setActiveId(item.id)}
              onFocus={()=>command.setActiveId(item.id)}
              onPointerEnter={()=>{setFocusRegion(false);setRegion(item.children?item.id:undefined)}}
                onClick={() => command.choose(item)}
              >
                <Link2 size={16} />
                <span>{item.label}</span>
              <small>{item.children?`${item.children.length} connectors`:item.id==='glean'?'Configure…':item.url.replace("https://", "")}</small>{item.children&&<ChevronRight size={12}/>}
            </button>
          </Popover.Anchor>{item.children&&<Popover.Portal><Popover.Content className="connector-menu connector-region-menu" data-flow-motion="floating" side="right" align="start" sideOffset={-1} onOpenAutoFocus={e=>{e.preventDefault();if(focusRegion)(e.target as HTMLElement).querySelector('button')?.focus()}}><div role="listbox" aria-label={`${item.label} region`}>{item.children.map(child=><button type="button" role="option" aria-selected={false} key={child.id} onKeyDown={e=>{e.stopPropagation();if(e.key==='ArrowLeft'||e.key==='Escape'){e.preventDefault();setRegion(undefined);command.inputRef.current?.focus()}if(e.key==='ArrowDown'){e.preventDefault();(e.currentTarget.nextElementSibling as HTMLElement)?.focus()}if(e.key==='ArrowUp'){e.preventDefault();(e.currentTarget.previousElementSibling as HTMLElement)?.focus()}}} onClick={()=>{setOpen(false);setRegion(undefined);onChoose(`${item.label} ${child.label}`,child.url)}}><span>{child.label}</span></button>)}</div></Popover.Content></Popover.Portal>}</Popover.Root>)}
            {!command.filteredOptions.length && <p>No connectors found</p>}
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

export function ConnectorDialog({
  initial,
  admin,
  onClose,
  onSaved,
}: {
  initial: { name: string; url: string };
  admin: boolean;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const [url, setURL] = useState(initial.url);
  const [name, setName] = useState(initial.name);
  const [shared, setShared] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const [headers, setHeaders] = useState([{ name: "", value: "" }]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [savedID, setSavedID] = useState("");
  const save = async () => {
    setBusy(true);
    setError("");
    try {
      const policy = savedID
        ? { id: savedID }
        : await request<{ id: string }>(
            "/api/application-policies",
            jsonRequest("PUT", {
              id: "",
              name: name.trim() || new URL(url).hostname,
              url,
              kind: "mcp",
              shared,
              status: admin ? "approved" : "pending",
            }),
          );
      setSavedID(policy.id);
      const configuredHeaders = Object.fromEntries(
        headers
          .filter((item) => item.name.trim())
          .map((item) => [item.name.trim(), item.value]),
      );
      if (Object.keys(configuredHeaders).length)
        await request(
          `/api/application-policies/${encodeURIComponent(policy.id)}/headers`,
          jsonRequest("PUT", { headers: configuredHeaders }),
        );
      await onSaved();
      onClose();
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Could not add connector",
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog
      open
      onOpenChange={(value) => {
        if (!value && !busy) onClose();
      }}
    >
      <DialogContent className="connector-dialog" aria-describedby={undefined}>
        <DialogTitle>
          {initial.name ? <><span>Add MCP connector</span> · <span data-i18n-ignore>{initial.name}</span></> : "Add custom MCP connector"}
        </DialogTitle>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <label>
            Connector URL
            <input
              type="url"
              required
              aria-label="Connector URL"
              placeholder="https://example.com/mcp"
              value={url}
              disabled={busy || Boolean(savedID)}
              onChange={(e) => setURL(e.target.value)}
              autoFocus
            />
          </label>
          {advanced && (
            <div className="connector-advanced">
              <label>
                Name
                <input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  disabled={busy || Boolean(savedID)}
                  maxLength={200}
                />
              </label>
              {admin && (
                <label className="connector-shared">
                  <input
                    type="checkbox"
                    checked={shared}
                    onChange={(e) => setShared(e.target.checked)}
                    disabled={busy || Boolean(savedID)}
                  />
                  Workspace connector
                </label>
              )}
              <strong>Authentication headers</strong>
              <small>
                Header values are only visible while configuring this connector
              </small>
              {headers.map((header, index) => (
                <div className="connector-header-inputs" key={index}>
                  <label>
                    Header name
                    <input
                      autoComplete="off"
                      value={header.name}
                      onChange={(e) =>
                        setHeaders((current) =>
                          current.map((item, i) =>
                            i === index
                              ? { ...item, name: e.target.value }
                              : item,
                          ),
                        )
                      }
                    />
                  </label>
                  <label>
                    Header value
                    <input
                      type="password"
                      autoComplete="new-password"
                      value={header.value}
                      onChange={(e) =>
                        setHeaders((current) =>
                          current.map((item, i) =>
                            i === index
                              ? { ...item, value: e.target.value }
                              : item,
                          ),
                        )
                      }
                    />
                  </label>
                  <button
                    type="button"
                    aria-label="Remove header"
                    onClick={() =>
                      setHeaders((current) =>
                        current.filter((_, i) => i !== index),
                      )
                    }
                  >
                    <Trash2 size={14} />
                  </button>
                </div>
              ))}
              <button
                type="button"
                className="settings-action"
                disabled={headers.length >= 16}
                onClick={() =>
                  setHeaders((current) => [...current, { name: "", value: "" }])
                }
              >
                <Plus size={14} />
                Add header
              </button>
            </div>
          )}
          {error && <p role="alert">{error}</p>}
          <footer>
            {!advanced && (
              <button
                className="settings-action"
                type="button"
                onClick={() => setAdvanced(true)}
              >
                Advanced…
                <ChevronRight size={14} />
              </button>
            )}
            <span />
            <button type="button" disabled={busy} onClick={onClose}>
              Cancel
            </button>
            <button type="submit" disabled={busy || !url}>
              Add connector
            </button>
          </footer>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function ConnectorAuthorization({ id }: { id: string }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [authorization,setAuthorization]=useState<{authorized:boolean;customHeaders:boolean}>();
  useEffect(()=>{let active=true;void request<{authorized:boolean;customHeaders:boolean}>(`/api/application-policies/${encodeURIComponent(id)}/authorization`).then(value=>{if(active)setAuthorization(value)}).catch(()=>{});return()=>{active=false}},[id]);
  const authorize = async () => {
    setBusy(true);
    setError("");
    try {
      const result = await request<{ authorizationURL: string }>(
        `/api/application-policies/${encodeURIComponent(id)}/oauth/start`,
        jsonRequest("POST", {}),
      );
      window.location.assign(result.authorizationURL);
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Authorization failed",
      );
      setBusy(false);
    }
  };
  return (
    <div className="connector-authorization">
      <button
        className="settings-action"
        disabled={busy}
        onClick={() => void authorize()}
      >
        {authorization?.authorized?'Reconnect':'Authorize'}
      </button>
      {(authorization?.authorized||authorization?.customHeaders)&&<button className="settings-action" disabled={busy} title="Disconnect authorization" aria-label="Disconnect authorization" onClick={async()=>{setBusy(true);try{await request(`/api/application-policies/${encodeURIComponent(id)}/authorization`,{method:'DELETE'});setAuthorization({authorized:false,customHeaders:false})}catch(reason){setError(String(reason))}finally{setBusy(false)}}}><Trash2 size={14}/></button>}
      {authorization?.authorized&&<small role="status">Authorized</small>}
      {error && <p role="alert">{error}</p>}
    </div>
  );
}
