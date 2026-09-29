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
it('renders loop-builder questions as answer chips and sends the picked option',async()=>{
 const question:ElicitationPrompt={id:'q1',sessionId:'session',connectorName:'Flow Agent',connectorUrl:'',mode:'form',message:'How much should the triage loop do on its own?',schema:{type:'object',properties:{answer:{type:'string',title:'How much should the triage loop do on its own?',enum:['Route and close clear duplicates',"Route, but don't close",'Suggest changes only']}},required:['answer']}};
 render(<AgentElicitation part={{id:'q1',type:'elicitation',status:'pending',elicitation:question}}/>);
 expect(screen.getByText('How much should the triage loop do on its own?')).toBeVisible();
 expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
 fireEvent.click(screen.getByRole('button',{name:"Route, but don't close"}));
 await waitFor(()=>expect(request).toHaveBeenCalledWith('/api/agent/sessions/session/elicitations/q1',{method:'POST',body:JSON.stringify({action:'accept',content:{answer:"Route, but don't close"}})}));
 expect(await screen.findByRole('button',{name:"Route, but don't close"})).toHaveAttribute('aria-pressed','true');
 expect(screen.getByRole('button',{name:'Suggest changes only'})).toBeDisabled();
});
it('shows the resolved answer of a stored loop-builder question',()=>{
 const question:ElicitationPrompt={id:'q2',sessionId:'session',connectorName:'Flow Agent',connectorUrl:'',mode:'form',message:'Skip any issues?',action:'accept',schema:{type:'object',properties:{answer:{type:'string',enum:['Review all','Skip already assigned']}},required:['answer']}};
 render(<AgentElicitation part={{id:'q2',type:'elicitation',status:'completed',text:'Skip already assigned',elicitation:question}}/>);
 expect(screen.getByRole('button',{name:'Skip already assigned'})).toHaveAttribute('aria-pressed','true');
 expect(screen.queryByRole('button',{name:'Skip'})).not.toBeInTheDocument();
});
it('inline (loop builder) questions are plain text with chips and no Skip, then quote the answer',async()=>{
 const question:ElicitationPrompt={id:'q3',sessionId:'session',connectorName:'Flow Agent',connectorUrl:'',mode:'form',message:'How much should the triage loop do on its own?',schema:{type:'object',properties:{answer:{type:'string',enum:['Route and close clear duplicates',"Route, but don't close"]}},required:['answer']}};
 const {container}=render(<AgentElicitation variant="inline" part={{id:'q3',type:'elicitation',status:'pending',elicitation:question}}/>);
 expect(container.querySelector('.agent-elicitation')).toBeNull();
 expect(container.querySelector('.agent-elicitation-inline')).not.toBeNull();
 expect(screen.getByText('How much should the triage loop do on its own?')).toBeVisible();
 expect(screen.queryByRole('button',{name:'Skip'})).not.toBeInTheDocument();
 fireEvent.click(screen.getByRole('button',{name:"Route, but don't close"}));
 const answer=await screen.findByRole('blockquote',{name:'Your answer'});
 expect(answer).toHaveClass('agent-elicitation-answer');
 expect(answer.querySelector('.agent-elicitation-answer-question')).toHaveTextContent('How much should the triage loop do on its own?');
 expect(answer.querySelector('.agent-elicitation-answer-value')).toHaveTextContent("Route, but don't close");
 expect(screen.queryByRole('button',{name:'Route and close clear duplicates'})).not.toBeInTheDocument();
});
it('inline questions restore a stored answer as the quoted bubble; the boxed style keeps Skip',()=>{
 const question:ElicitationPrompt={id:'q4',sessionId:'session',connectorName:'Flow Agent',connectorUrl:'',mode:'form',message:'Skip any issues?',action:'accept',schema:{type:'object',properties:{answer:{type:'string',enum:['Review all','Skip already assigned']}},required:['answer']}};
 render(<AgentElicitation variant="inline" part={{id:'q4',type:'elicitation',status:'completed',text:'Skip already assigned',elicitation:question}}/>);
 expect(screen.getByRole('blockquote',{name:'Your answer'})).toHaveTextContent('Skip any issues?Skip already assigned');
 render(<AgentElicitation part={{id:'q5',type:'elicitation',status:'pending',elicitation:{...question,id:'q5',action:undefined}}}/>);
 expect(screen.getByRole('button',{name:'Skip'})).toBeVisible();
});
