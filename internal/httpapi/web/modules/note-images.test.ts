import { readFileSync } from "node:fs";
import path from "node:path";
import MarkdownIt from "markdown-it";
import { describe, expect, it } from "vitest";
import {
  MAX_NOTE_IMAGES,
  NOTE_IMAGE_MAX_WIDTH,
  NOTE_IMAGE_MIN_WIDTH,
  clampImageWidth,
  findNoteImageRefs,
  formatImageAlt,
  parseImageAlt,
} from "./note-images.js";

const U1 = "11111111-1111-4111-8111-111111111111";
const U2 = "22222222-2222-4222-9222-222222222222";
const img = (uuid: string, alt = "shot") => `![${alt}](/api/assets/${uuid})`;

// Same configuration as the preview renderer (markdown-preview.ts).
const md = new MarkdownIt("default", { html: false, breaks: true, linkify: false });
const refs = (text: string) => findNoteImageRefs(text, md.parse(text, {}));
const spans = (text: string) => refs(text).map((r) => text.slice(r.from, r.to));

describe("image display width in alt text", () => {
  it.each([
    ["shot|480", "shot", 480],
    ["|320", "", 320],
    ["a|b|240", "a|b", 240],
    [`shot|${NOTE_IMAGE_MIN_WIDTH}`, "shot", NOTE_IMAGE_MIN_WIDTH],
    [`shot|${NOTE_IMAGE_MAX_WIDTH}`, "shot", NOTE_IMAGE_MAX_WIDTH],
  ])("parses %s", (alt, want, width) => {
    expect(parseImageAlt(alt)).toEqual({ alt: want, width });
  });

  it.each(["shot", "shot|47", "shot|1601", "shot|0480", "shot|480 ", "shot|4800", "Q3|2024", "shot|12a", "shot| 480"])(
    "treats %s as plain alt text",
    (alt) => {
      expect(parseImageAlt(alt)).toEqual({ alt, width: null });
    },
  );

  it("formats and round-trips", () => {
    expect(formatImageAlt("shot", 480)).toBe("shot|480");
    expect(formatImageAlt("shot", null)).toBe("shot");
    for (const width of [NOTE_IMAGE_MIN_WIDTH, 320, NOTE_IMAGE_MAX_WIDTH]) {
      expect(parseImageAlt(formatImageAlt("a|b", width))).toEqual({ alt: "a|b", width });
    }
  });

  it("clamps widths to the stored range and a ceiling", () => {
    expect(clampImageWidth(10)).toBe(NOTE_IMAGE_MIN_WIDTH);
    expect(clampImageWidth(99999)).toBe(NOTE_IMAGE_MAX_WIDTH);
    expect(clampImageWidth(300.6)).toBe(301);
    expect(clampImageWidth(900, 500)).toBe(500);
    expect(clampImageWidth(900, 20)).toBe(NOTE_IMAGE_MIN_WIDTH);
  });
});

describe("visual editor image references", () => {
  it("finds plain and sized references with exact offsets", () => {
    const text = `Intro\n\n${img(U1)}\n\nThen ${img(U2, "b|480")} inline.`;
    const found = refs(text);
    expect(found.map((r) => [r.path, r.alt, r.width])).toEqual([
      [`/api/assets/${U1}`, "shot", null],
      [`/api/assets/${U2}`, "b", 480],
    ]);
    expect(text.slice(found[1].from, found[1].to)).toBe(img(U2, "b|480"));
    expect(text.slice(found[1].altFrom, found[1].altTo)).toBe("b|480");
  });

  it("leaves references in code, behind escapes, and in non-image positions as text", () => {
    expect(spans("```\n" + img(U1) + "\n```")).toEqual([]);
    expect(spans("~~~mermaid\n" + img(U1) + "\n~~~")).toEqual([]);
    expect(spans("    " + img(U1))).toEqual([]);
    expect(spans("`" + img(U1) + "`")).toEqual([]);
    expect(spans("``a ` " + img(U1) + "``")).toEqual([]);
    expect(spans("\\" + img(U1))).toEqual([]);
    expect(spans("`unclosed " + img(U1))).toEqual([img(U1)]);
    expect(spans("\\\\" + img(U1))).toEqual([img(U1)]);
  });

  it("only edits the simple uploader form", () => {
    expect(spans(`![a](/api/assets/${U1} "title")`)).toEqual([]);
    expect(spans(`![a](</api/assets/${U1}>)`)).toEqual([]);
    expect(spans(`![a [b]](/api/assets/${U1})`)).toEqual([]);
    expect(spans(`![a][ref]\n\n[ref]: /api/assets/${U1}`)).toEqual([]);
    expect(spans(`![a](/api/assets/${"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa".toUpperCase()})`)).toEqual([]);
    expect(spans(`![a](/api/assets/${U1}?x=1)`)).toEqual([]);
    expect(spans(`![a](https://example.com/x.png)`)).toEqual([]);
  });

  it("handles block containers, links, tables and repeats", () => {
    expect(spans(`> quote ${img(U1)}`)).toEqual([img(U1)]);
    expect(spans(`- item\n  ${img(U1)}`)).toEqual([img(U1)]);
    expect(spans(`[${img(U1)}](https://example.com)`)).toEqual([img(U1)]);
    expect(spans(`# ${img(U1)}`)).toEqual([img(U1)]);
    const table = `| a | b |\n| - | - |\n| ${img(U1)} | ${img(U1)} |`;
    const found = refs(table);
    expect(found).toHaveLength(2);
    expect(new Set(found.map((r) => r.from)).size).toBe(2);
    expect(spans(`${img(U1)} ${img(U1)}`)).toEqual([img(U1), img(U1)]);
  });

  it("uses UTF-16 offsets around non-ASCII text", () => {
    const text = `😀 Ünïcode ${img(U1)} 中文`;
    const [ref] = refs(text);
    expect(text.slice(ref.from, ref.to)).toBe(img(U1));
  });

  it("caps the number of images shown", () => {
    const text = Array.from({ length: MAX_NOTE_IMAGES + 5 }, () => img(U1)).join("\n\n");
    expect(refs(text)).toHaveLength(MAX_NOTE_IMAGES);
  });

  // The editor must never show an image the server does not count as a reference (it would hide
  // text the server treats as text). Every editor reference must be in the server's extraction.
  it("never shows a reference the server extractor ignores (assetrefs parity)", () => {
    const fixturesPath = path.resolve(process.cwd(), "../../assetrefs/testdata/fixtures.json");
    const fixtures = JSON.parse(readFileSync(fixturesPath, "utf8")) as Array<{ name: string; markdown: string; expected: string[] }>;
    let shown = 0;
    for (const f of fixtures) {
      const text = f.markdown.replace(/\r\n?/g, "\n");
      for (const ref of refs(text)) {
        shown += 1;
        expect(f.expected, f.name).toContain(ref.path.slice("/api/assets/".length));
      }
    }
    expect(shown).toBeGreaterThan(5);
  });
});
