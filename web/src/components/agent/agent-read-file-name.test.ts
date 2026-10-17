import { describe, expect, it } from "vitest";
import { readFileName } from "./agent-step-labels";

const attachments = [
  { name: "report.pdf", url: "/uploads/abc/report.pdf", id: "att_1" },
  { name: "notes.txt" },
];

describe("readFileName", () => {
  it("returns empty for non-string or blank refs", () => {
    expect(readFileName(undefined, attachments)).toBe("");
    expect(readFileName(42, attachments)).toBe("");
    expect(readFileName("   ", attachments)).toBe("");
  });

  it("resolves an exact url, name or id match to the attachment name", () => {
    expect(readFileName("/uploads/abc/report.pdf", attachments)).toBe("report.pdf");
    expect(readFileName("notes.txt", attachments)).toBe("notes.txt");
    expect(readFileName("att_1", attachments)).toBe("report.pdf");
  });

  it("matches a url that only differs by origin prefix", () => {
    expect(readFileName("https://x.test/uploads/abc/report.pdf", attachments)).toBe("report.pdf");
  });

  it("falls back to the last path segment for unknown urls", () => {
    expect(readFileName("/uploads/zzz/My%20Sheet.xlsx?sig=1#top")).toBe("My Sheet.xlsx");
    expect(readFileName("/uploads/zzz/other.csv", attachments)).toBe("other.csv");
  });

  it("does not match a short reference to an attachment whose url merely ends with it", () => {
    expect(readFileName("pdf", attachments)).toBe("pdf");
    expect(readFileName("report.pdf", attachments)).toBe("report.pdf");
  });

  it("returns a bare unknown name unchanged", () => {
    expect(readFileName("plain.md", attachments)).toBe("plain.md");
  });

  it("keeps the raw segment when percent-encoding is malformed", () => {
    expect(readFileName("/uploads/zzz/bad%E0%A4%A.txt")).toBe("bad%E0%A4%A.txt");
  });
});
