import { afterEach, expect, it, vi } from "vitest";
import { chatIdentityAuthorizationURL, fetchChatIntegrationChats, removeChatBinding, saveChatIntegration, startChatIdentityBinding } from "./chat-integrations-api";

afterEach(() => vi.unstubAllGlobals());

it("uses dedicated provider endpoints, encoded cursors and JSON credentials", async () => {
  const fetch = vi.fn().mockImplementation(async () => new Response("{}", { headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", fetch);
  await fetchChatIntegrationChats("lark", "cursor+next/=");
  expect(fetch.mock.calls[0][0]).toBe("/api/integrations/lark/chat/chats?cursor=cursor%2Bnext%2F%3D");
  await saveChatIntegration("feishu", { appId: "app-1", appSecret: "secret" });
  expect(fetch.mock.calls[1][0]).toBe("/api/integrations/feishu/chat");
  expect(fetch.mock.calls[1][1]).toEqual(expect.objectContaining({ method: "PUT", body: JSON.stringify({ appId: "app-1", appSecret: "secret" }) }));
  await removeChatBinding("lark", "binding/1");
  expect(fetch.mock.calls[2][0]).toBe("/api/integrations/lark/chat/bindings/binding%2F1");
  await startChatIdentityBinding("feishu");
  expect(fetch.mock.calls[3][0]).toBe("/api/integrations/feishu/chat/identity/start");
  expect(fetch.mock.calls[3][1]).toEqual(expect.objectContaining({ method: "POST" }));
});

it("only follows authorization links on the selected platform", () => {
  expect(chatIdentityAuthorizationURL("feishu", "https://accounts.feishu.cn/open-apis/authen/v1/authorize?state=abc")).toContain("state=abc");
  expect(chatIdentityAuthorizationURL("lark", "https://accounts.larksuite.com/open-apis/authen/v1/authorize")).toContain("accounts.larksuite.com");
  for (const value of ["javascript:alert(1)", "http://accounts.feishu.cn/oauth", "https://accounts.feishu.cn.evil.example/oauth", "https://user:password@accounts.feishu.cn/oauth", "https://accounts.feishu.cn:444/oauth", "https://accounts.larksuite.com/oauth"]) {
    expect(() => chatIdentityAuthorizationURL("feishu", value)).toThrow();
  }
});
