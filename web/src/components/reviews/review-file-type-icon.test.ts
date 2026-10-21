import { describe, expect, it } from "vitest";
import { fileTypeGlyph } from "./review-file-type-icon";

describe("fileTypeGlyph", () => {
  it("picks the glyph by extension, case-insensitively", () => {
    expect(fileTypeGlyph("src/greet.js").paths[0][0]).toBe("#ffca28");
    expect(fileTypeGlyph("web/App.TSX").paths[0][0]).toBe("#0288d1");
    expect(fileTypeGlyph("api/main.go").paths[0][0]).toBe("#00acc1");
  });

  it("falls back to the generic file glyph", () => {
    expect(fileTypeGlyph("Makefile")).toBe(fileTypeGlyph("notes.unknown"));
    expect(fileTypeGlyph("Makefile").paths[0][0]).toBe("#90a4ae");
  });
});
