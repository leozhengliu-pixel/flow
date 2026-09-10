import {fireEvent,render,screen,waitFor} from '@testing-library/react';
import {beforeEach,expect,it,vi} from 'vitest';
import {AgentElicitation,type ElicitationPrompt} from './agent-elicitation';
const {request}=vi.hoisted(()=>({request:vi.fn()}));
vi.mock('@/lib/api-client',()=>({request,jsonRequest:(method:string,input:unknown)=>({method,body:JSON.stringify(input)})}));
beforeEach(()=>{vi.clearAllMocks();request.mockResolvedValue({action:'accept'})});
const prompt:ElicitationPrompt={id:'form',sessionId:'session',connectorName:'Support',connectorUrl:'https://support.example.test/mcp',mode:'form',message:'Choose a region',schema:{type:'object',properties:{region:{type:'string',enum:['eu','us'],default:'eu'},count:{type:'integer',minimum:1}},required:['region','count']}};
it('renders attributed form fields and sends their typed values only after submission',async()=>{
 render(<AgentElicitation part={{id:'form',type:'elicitation',status:'pending',elicitation:prompt}}/>);
 expect(screen.getByText('support.example.test')).toBeVisible();expect(request).not.toHaveBeenCalled();
 fireEvent.change(screen.getByRole('spinbutton'),{target:{value:'2'}});fireEvent.click(screen.getByRole('button',{name:'Submit'}));
 await waitFor(()=>expect(request).toHaveBeenCalledWith('/api/agent/sessions/session/elicitations/form',{method:'POST',body:JSON.stringify({action:'accept',content:{region:'eu',count:2}})}));
 expect(await screen.findByRole('status')).toHaveTextContent('Response sent');
});
it('keeps an invalid form editable and can decline without transmitting answers',async()=>{
 request.mockRejectedValueOnce(new Error('Response does not satisfy the requested form'));
 render(<AgentElicitation part={{id:'form',type:'elicitation',status:'pending',elicitation:prompt}}/>);
 fireEvent.change(screen.getByRole('spinbutton'),{target:{value:'3'}});fireEvent.click(screen.getByRole('button',{name:'Submit'}));
 expect(await screen.findByRole('alert')).toHaveTextContent('Response does not satisfy');
 fireEvent.click(screen.getByRole('button',{name:'Decline'}));await waitFor(()=>expect(request).toHaveBeenLastCalledWith(expect.any(String),{method:'POST',body:JSON.stringify({action:'decline'})}));
});
it('URL mode opens a separate confirmation destination without asking for credentials',()=>{
 render(<AgentElicitation part={{id:'url',type:'elicitation',status:'pending',elicitation:{...prompt,mode:'url',url:'https://support.example.test/confirm',schema:undefined}}}/>);
 expect(screen.getByRole('link')).toHaveAttribute('href','https://support.example.test/confirm');expect(screen.queryByRole('textbox')).not.toBeInTheDocument();expect(request).not.toHaveBeenCalled();
});
