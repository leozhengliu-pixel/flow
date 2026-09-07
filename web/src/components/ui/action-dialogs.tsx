import { useEffect, useRef, useState } from "react";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { completeActionDialog, currentActionDialog, subscribeActionDialogs, type DialogRequest } from "./action-dialog-service";

import "./action-dialogs.css";

export function ActionDialogHost() {
  const [request, setRequest] = useState<DialogRequest>();
  const [value, setValue] = useState("");
  const lastRequest = useRef<DialogRequest | undefined>(request);
  if (request) lastRequest.current = request;
  const displayRequest = request ?? lastRequest.current;
  useEffect(() => {
    const sync = () => setRequest(current => current ?? currentActionDialog());
    const unsubscribe=subscribeActionDialogs(sync); sync();
    return unsubscribe;
  }, []);
  useEffect(() => { if (request?.kind === "prompt") setValue(request.defaultValue); }, [request]);
  const finish = (result: boolean | string | null) => {
    completeActionDialog(result);
    setRequest(undefined);
  };
  return <Dialog open={Boolean(request)} onOpenChange={open => !open && finish(request?.kind === "confirm" ? false : null)}><DialogContent className="action-dialog" aria-hidden={!request || undefined} inert={!request || undefined} onOpenAutoFocus={event=>{if(request?.kind!=="prompt")return;event.preventDefault();requestAnimationFrame(()=>document.querySelector<HTMLInputElement>('.action-dialog input')?.focus())}}><DialogTitle>{displayRequest?.title}</DialogTitle>{displayRequest?.description&&<p>{displayRequest.description}</p>}{displayRequest?.kind === "prompt"&&<input aria-label={displayRequest.title} onChange={event=>setValue(event.target.value)} onKeyDown={event=>{if(event.key==='Enter'&&value.trim()){event.preventDefault();finish(value.trim())}}} value={value}/>}<footer><button disabled={!request} onClick={()=>finish(request?.kind==='confirm'?false:null)} type="button">Cancel</button><button className={displayRequest?.kind==='confirm'&&displayRequest.danger?'danger':'primary'} disabled={!request || (request.kind==='prompt'&&!value.trim())} onClick={()=>finish(request?.kind==='prompt'?value.trim():true)} type="button">{displayRequest?.confirmLabel}</button></footer></DialogContent></Dialog>;
}
