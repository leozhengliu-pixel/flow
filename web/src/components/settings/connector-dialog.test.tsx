import {render,screen,within,waitFor} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {expect,it,vi} from 'vitest';
import {ConnectorMenu} from './connector-dialog';
it('selects Custom URL after a direct label match instead of a fuzzy hostname match',async()=>{
 const user=userEvent.setup();const choose=vi.fn();render(<ConnectorMenu onChoose={choose}/>);
 await user.click(screen.getByRole('button',{name:'Add connector'}));await user.type(screen.getByRole('searchbox',{name:'Search connectors'}),'Custom');await user.keyboard('{Enter}');
 expect(choose).toHaveBeenCalledWith('','');
});
it('opens a regional submenu with the keyboard and chooses the focused child',async()=>{
 const user=userEvent.setup();const choose=vi.fn();render(<ConnectorMenu onChoose={choose}/>);
 await user.click(screen.getByRole('button',{name:'Add connector'}));await user.type(screen.getByRole('searchbox',{name:'Search connectors'}),'Amplitude');await user.keyboard('{Enter}');
 const list=await screen.findByRole('listbox',{name:'Amplitude region'});
 await waitFor(()=>expect(within(list).getByRole('option',{name:'US'})).toHaveFocus());await user.keyboard('{ArrowDown}{Enter}');
 expect(choose).toHaveBeenCalledWith('Amplitude EU','https://mcp.eu.amplitude.com/mcp');
});
