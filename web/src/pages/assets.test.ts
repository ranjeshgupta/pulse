import { describe as suite, expect, it } from "vitest";
import { downloadSnippet, fileExt, formatSize, titleFromFilename, typeLabel, validateFile } from "./Assets";

const EXT = [".pdf", ".png", ".docx"];
const MB = 1024 * 1024;

suite("asset helpers", () => {
  it("formats sizes", () => {
    expect(formatSize(0)).toBe("0 bytes");
    expect(formatSize(1)).toBe("1 byte");
    expect(formatSize(830 * 1024)).toBe("830 KB");
    expect(formatSize(2.4 * MB)).toBe("2.4 MB");
    expect(formatSize(25 * MB)).toBe("25 MB");
    expect(formatSize(2048 * MB)).toBe("2 GB");
  });

  it("reads extensions and default titles", () => {
    expect(fileExt("Price List.PDF")).toBe(".pdf");
    expect(fileExt(".env")).toBe("");
    expect(fileExt("README")).toBe("");
    expect(titleFromFilename("report.2026.pdf")).toBe("report.2026");
    expect(typeLabel("a.docx")).toBe("DOCX");
    expect(typeLabel("blob", "image/png")).toBe("PNG");
  });

  it("checks files like the server does", () => {
    expect(validateFile({ name: "a.pdf", size: 10 }, 25 * MB, EXT)).toBeNull();
    expect(validateFile({ name: "A.PNG", size: 10 }, 25 * MB, EXT)).toBeNull();
    expect(validateFile({ name: "a.exe", size: 10 }, 25 * MB, EXT)).toMatch(/\.exe files aren't allowed/);
    expect(validateFile({ name: "noext", size: 10 }, 25 * MB, EXT)).toMatch(/without an extension/);
    expect(validateFile({ name: "big.pdf", size: 26 * MB }, 25 * MB, EXT)).toMatch(/limit is 25 MB/);
    expect(validateFile({ name: "e.pdf", size: 0 }, 25 * MB, EXT)).toMatch(/empty/);
  });

  it("escapes the HTML snippet", () => {
    expect(downloadSnippet("https://x.test/a/k", 'Q&A "guide" <v2>')).toBe('<a href="https://x.test/a/k">Download Q&amp;A &quot;guide&quot; &lt;v2&gt;</a>');
  });
});
