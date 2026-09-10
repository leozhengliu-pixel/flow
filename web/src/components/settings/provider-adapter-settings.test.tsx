import {fireEvent,render,screen,waitFor} from '@testing-library/react';
import {beforeEach,expect,it,vi} from 'vitest';
import {ProviderAdapterSettings} from './provider-adapter-settings';
import {makeBootstrap} from '@/test/fixtures';
const {request}=vi.hoisted(()=>({request:vi.fn()}));
vi.mock('@/lib/api-client',()=>({request,jsonRequest:(method:string,input:unknown)=>({method,body:JSON.stringify(input)})}));
vi.mock('@/lib/api',()=>({disconnectIntegration:vi.fn()}));
beforeEach(()=>{vi.clearAllMocks()});
it('does not mark an integration connected when token verification fails',async()=>{
 request.mockRejectedValue(new Error('Sentry returned HTTP 401'));
 render(<ProviderAdapterSettings provider="sentry" name="Sentry" data={makeBootstrap({integrationConnections:[]})} onClose={vi.fn()} onReload={vi.fn()}/>);
 fireEvent.change(screen.getByLabelText('Sentry access token'),{target:{value:'test-token'}});fireEvent.click(screen.getByRole('button',{name:'Verify and connect'}));
 expect(await screen.findByRole('alert')).toHaveTextContent('401');expect(screen.getByLabelText('Sentry access token')).toBeVisible();
});
it('submits a Sentry import to the chosen team and renders its actual result',async()=>{
 request.mockResolvedValue({issueId:'issue-result',title:'Error report'});
 const data=makeBootstrap();data.integrationConnections=[{id:'sentry',provider:'sentry',name:'Sentry',status:'connected',connectedBy:data.viewer.id,createdAt:'2026-09-10',updatedAt:'2026-09-10',scopes:[],channels:[],linkbackEnabled:false,deliveryAttempts:0}];
 render(<ProviderAdapterSettings provider="sentry" name="Sentry" data={data} onClose={vi.fn()} onReload={vi.fn()}/>);
 fireEvent.change(screen.getByLabelText('Sentry issue ID'),{target:{value:'42'}});fireEvent.click(screen.getByRole('button',{name:'Create linked issue'}));
 await waitFor(()=>expect(request).toHaveBeenCalledWith('/api/integrations/sentry/actions',expect.objectContaining({body:expect.stringContaining('"action":"import"')})));
 expect(await screen.findByText('Error report')).toBeVisible();
});
