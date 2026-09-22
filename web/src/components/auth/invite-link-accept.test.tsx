import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { fetchInviteLinkPreview } from "@/lib/api";
import { I18nProvider } from "@/i18n/i18n";
import { InviteLinkAccept } from "./invite-link-accept";

vi.mock("@/lib/api", () => ({
  fetchInviteLinkPreview: vi.fn(),
  joinOrganization: vi.fn(),
  logoutAccount: vi.fn(),
}));

describe("InviteLinkAccept", () => {
  beforeEach(() => {
    vi.mocked(fetchInviteLinkPreview).mockReset();
  });

  it("redirects already-member previews", async () => {
    const onJoined = vi.fn().mockResolvedValue(undefined);
    vi.mocked(fetchInviteLinkPreview).mockResolvedValue({
      token: "tok",
      alreadyMember: true,
      allowedAuthServices: ["email"],
      workspace: {
        id: "ws",
        name: "Acme",
        urlKey: "acme",
        createdAt: "2026-01-01T00:00:00Z",
      } as never,
    });
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/join/tok"]}>
          <Routes>
            <Route
              path="/join/:token"
              element={<InviteLinkAccept session={null} onJoined={onJoined} />}
            />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );
    await waitFor(() => expect(onJoined).toHaveBeenCalledWith("acme"));
  });

  it("shows join CTA for signed-in non-members", async () => {
    vi.mocked(fetchInviteLinkPreview).mockResolvedValue({
      token: "tok",
      alreadyMember: false,
      allowedAuthServices: ["email"],
      workspace: {
        id: "ws",
        name: "Acme",
        urlKey: "acme",
        createdAt: "2026-01-01T00:00:00Z",
      } as never,
    });
    render(
      <I18nProvider>
        <MemoryRouter initialEntries={["/join/tok"]}>
          <Routes>
            <Route
              path="/join/:token"
              element={
                <InviteLinkAccept
                  session={
                    {
                      user: {
                        id: "u1",
                        name: "Ada",
                        displayName: "Ada",
                        email: "ada@example.com",
                        active: true,
                      },
                      memberships: [],
                      expiresAt: "2099-01-01T00:00:00Z",
                    } as never
                  }
                  onJoined={vi.fn()}
                />
              }
            />
          </Routes>
        </MemoryRouter>
      </I18nProvider>,
    );
    expect(
      await screen.findByRole("button", { name: "Join workspace" }),
    ).toBeVisible();
  });
});
