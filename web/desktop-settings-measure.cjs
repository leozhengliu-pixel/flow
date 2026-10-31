const { chromium } = require('playwright');
const fs = require('node:fs');
const routes = { preferences:'account/preferences', profile:'account/profile', notifications:'account/notifications', 'account-security':'account/security', workspace:'workspace', members:'members', teams:'teams', 'team-settings':'teams/VER', 'issue-labels':'issue-labels', 'project-labels':'project-labels', 'project-statuses':'project-statuses', 'issue-templates':'issue-templates', 'project-templates':'project-templates', integrations:'integrations', api:'api', billing:'billing', 'workspace-security':'security' };
(async () => {
 const browser = await chromium.launch();
 const context = await browser.newContext({viewport:{width:1440,height:900},deviceScaleFactor:2,colorScheme:'dark'});
 const page = await context.newPage();
 const result = {};
 for (const [name, route] of Object.entries(routes)) {
  await page.goto(`http://127.0.0.1:5463/verify/settings/${route}`, {waitUntil:'networkidle'});
  await page.waitForTimeout(250);
  result[name] = await page.evaluate(() => Array.from(document.querySelectorAll('.settings-content h1,.settings-content h2,.settings-content h3,.settings-content input,.settings-content button,.settings-content a,.settings-content .settings-card,.settings-content .personal-card,.settings-content .settings-row,.settings-content .settings-row-icon,.settings-content .personal-channel-icon,.settings-content .settings-members-columns > *,.settings-content .label-settings-column,.settings-content .feature-integration-search,.settings-content .feature-integration-section,.settings-content .team-overview-icon,.settings-content .team-overview-summary,.settings-content .personal-static,.settings-sidebar-footer,.settings-plan-button,.settings-back,.settings-search,.settings-sidebar nav h2,.settings-sidebar nav button')).map(e=>{const r=e.getBoundingClientRect(),s=getComputedStyle(e);return {tag:e.tagName,cls:e.className,text:(e.textContent||e.getAttribute('aria-label')||'').trim().slice(0,120),rect:[r.x,r.y,r.width,r.height],font:[s.fontSize,s.fontWeight,s.lineHeight],padding:s.padding,radius:s.borderRadius,display:s.display}}));
 }
 fs.writeFileSync(`/tmp/codex-desktop-settings/${process.argv[2]||'before'}.json`,JSON.stringify(result,null,2));
 await browser.close();
})();
