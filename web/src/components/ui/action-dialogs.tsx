import { useEffect, useState } from "react";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { completeActionDialog, currentActionDialog, subscribeActionDialogs, type DialogRequest } from "./action-dialog-service";

import "./action-dialogs.css";

export function ActionDialogHost() {
  const [request, setRequest] = useState<DialogRequest>();
  const [value, setValue] = useState("");
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
  return <Dialog open={Boolean(request)} onOpenChange={open => !open && finish(request?.kind === "confirm" ? false : null)}><DialogContent className="action-dialog" onOpenAutoFocus={event=>{if(request?.kind!=="prompt")return;event.preventDefault();requestAnimationFrame(()=>document.querySelector<HTMLInputElement>('.action-dialog input')?.focus())}}><DialogTitle>{request?.title}</DialogTitle>{request?.description&&<p>{request.description}</p>}{request?.kind === "prompt"&&<input aria-label={request.title} onChange={event=>setValue(event.target.value)} onKeyDown={event=>{if(event.key==='Enter'&&value.trim()){event.preventDefault();finish(value.trim())}}} value={value}/>}<footer><button onClick={()=>finish(request?.kind==='confirm'?false:null)} type="button">Cancel</button><button className={request?.kind==='confirm'&&request.danger?'danger':'primary'} disabled={request?.kind==='prompt'&&!value.trim()} onClick={()=>finish(request?.kind==='prompt'?value.trim():true)} type="button">{request?.confirmLabel}</button></footer></DialogContent></Dialog>;
}
