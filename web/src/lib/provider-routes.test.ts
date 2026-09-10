import {expect,it} from 'vitest';
import {integrationSettingsPath,parseAppRoute,type IntegrationProvider} from './app-routes';
it('round-trips every supported provider through a persistent settings route',()=>{
 const providers:IntegrationProvider[]=['github','gitlab'];
 for(const provider of providers){const path=integrationSettingsPath('workspace',provider);expect(parseAppRoute(path)).toEqual({kind:'settings',workspaceSlug:'workspace',page:'integrations',integrationProvider:provider})}
});
it('does not resolve removed provider setup pages',()=>{
 for(const provider of ['notion','intercom','sentry','figma','google-calendar','cursor','codex','zapier']) {
  expect(parseAppRoute(`/workspace/settings/integrations/${provider}`)).not.toMatchObject({kind:'settings',integrationProvider:provider});
 }
});
