// Shared rules for Scrumboy-managed images in story notes: the canonical destination, the
// optional display width stored in the alt text, and which references in a note's Markdown are
// real images (as opposed to text inside code) that the visual notes editor may show as images.

/** The only image destination that renders. Must match internal/assetrefs (lowercase UUIDv4). */
export const CANONICAL_ASSET_PATH_RE = /^\/api\/assets\/[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

/** Stored widths are whole CSS pixels in this range; anything else is ordinary alt text. */
export const NOTE_IMAGE_MIN_WIDTH = 48;
export const NOTE_IMAGE_MAX_WIDTH = 1600;
/** Without a stored width an image is shown at most this large, and never enlarged. */
export const NOTE_IMAGE_DEFAULT_MAX_WIDTH = 320;
export const NOTE_IMAGE_DEFAULT_MAX_HEIGHT = 240;
/** Same cap as the preview: further images stay Markdown text. */
export const MAX_NOTE_IMAGES = 50;

// The width suffix: "|" then 2-4 digits without a leading zero, ending the alt text.
const WIDTH_SUFFIX_RE = /\|([1-9][0-9]{1,3})$/;
const DRAWING_SUFFIX_RE = /\|draw:(drw_[A-Za-z0-9_-]{6,64})$/;

export const CANONICAL_DRAWING_PATH_RE = /^\/api\/board\/([A-Za-z0-9_-]{1,128})\/drawings\/(drw_[A-Za-z0-9_-]{6,64})$/;

export type ParsedImageAlt = { alt: string; width: number | null; drawingId: string | null };

export function parseImageAlt(rawAlt: string): ParsedImageAlt {
  let text = rawAlt;
  let width: number | null = null;
  const match = WIDTH_SUFFIX_RE.exec(text);
  if (match) {
    const value = Number(match[1]);
    if (value >= NOTE_IMAGE_MIN_WIDTH && value <= NOTE_IMAGE_MAX_WIDTH) {
      width = value;
      text = text.slice(0, match.index);
    }
  }
  let drawingId: string | null = null;
  const draw = DRAWING_SUFFIX_RE.exec(text);
  if (draw) {
    drawingId = draw[1];
    text = text.slice(0, draw.index);
  }
  return { alt: text, width, drawingId };
}

export function formatImageAlt(alt: string, width: number | null, drawingId: string | null = null): string {
  let out = alt;
  if (drawingId) out += `|draw:${drawingId}`;
  if (width != null) out += `|${width}`;
  return out;
}

export function drawingPath(slug: string, drawingId: string): string {
  return `/api/board/${slug}/drawings/${drawingId}`;
}

export function parseDrawingPath(path: string): { slug: string; drawingId: string } | null {
  const m = CANONICAL_DRAWING_PATH_RE.exec(path);
  return m ? { slug: m[1], drawingId: m[2] } : null;
}

export function isRenderableMediaPath(path: string): boolean {
  return CANONICAL_ASSET_PATH_RE.test(path) || CANONICAL_DRAWING_PATH_RE.test(path);
}

/** Rounds and clamps a width to the stored range and an optional smaller ceiling (>= minimum). */
export function clampImageWidth(width: number, ceiling: number = NOTE_IMAGE_MAX_WIDTH): number {
  const max = Math.max(NOTE_IMAGE_MIN_WIDTH, Math.min(NOTE_IMAGE_MAX_WIDTH, Math.floor(ceiling)));
  return Math.min(max, Math.max(NOTE_IMAGE_MIN_WIDTH, Math.round(width)));
}

/** A reference the visual editor can show as an image, in offsets of the LF-joined note text. */
export type NoteImageRef = {
  from: number;
  to: number;
  /** Offsets of the raw alt text between the brackets. */
  altFrom: number;
  altTo: number;
  path: string;
  alt: string;
  width: number | null;
  drawingId: string | null;
  kind: "asset" | "drawing";
};

/** The subset of a markdown-it block token the scanner reads. */
export type MarkdownBlockToken = {
  type: string;
  map?: [number, number] | null;
  children?: Array<{ type: string; attrGet?(name: string): string | null }> | null;
};

// Only the simple form the uploader writes is edited visually: inline destination, no title, and
// alt text without brackets, backslashes, backticks or line breaks. Other forms stay text.
const EDITABLE_IMAGE_RE = /!\[([^[\]\\`\r\n]*)\]\((\/api\/(?:assets\/[0-9a-f-]{36}|board\/[A-Za-z0-9_-]{1,128}\/drawings\/drw_[A-Za-z0-9_-]{6,64}))\)/g;

// codeSpanRanges returns [start, end) offsets of CommonMark code spans in text: a backtick string
// opens a span closed by the next backtick string of the same length; unmatched ones are text.
// Outside code spans a backslash escapes the next character.
function codeSpanRanges(text: string): Array<[number, number]> {
  const spans: Array<[number, number]> = [];
  let i = 0;
  while (i < text.length) {
    const ch = text[i];
    if (ch === "\\") {
      i += 2;
      continue;
    }
    if (ch !== "`") {
      i += 1;
      continue;
    }
    let run = 1;
    while (text[i + run] === "`") run += 1;
    let j = i + run;
    let close = -1;
    while (j < text.length) {
      if (text[j] !== "`") {
        j += 1;
        continue;
      }
      let len = 1;
      while (text[j + len] === "`") len += 1;
      if (len === run) {
        close = j;
        break;
      }
      j += len;
    }
    if (close < 0) {
      i += run;
      continue;
    }
    spans.push([i, close + run]);
    i = close + run;
  }
  return spans;
}

function lineStarts(text: string): number[] {
  const starts = [0];
  for (let i = 0; i < text.length; i += 1) {
    if (text.charCodeAt(i) === 10) starts.push(i + 1);
  }
  return starts;
}

/**
 * Finds the image references in note text (lines joined with "\n") that render as Scrumboy
 * images. tokens are markdown-it block tokens for the same text, so references in fenced or
 * indented code, inline code, behind a backslash, or anywhere markdown-it does not see an image
 * are left out. A reference is only reported when markdown-it found a matching image in its
 * block, so a doubtful case stays text instead of being hidden behind an image.
 */
export function findNoteImageRefs(text: string, tokens: MarkdownBlockToken[]): NoteImageRef[] {
  const starts = lineStarts(text);
  const refs: NoteImageRef[] = [];
  // Table cells share their row's line range, so one reference can be seen by several tokens.
  const used = new Set<number>();
  // Table cells have no line map of their own; they use their row's (the last map seen).
  let lastMap: [number, number] | null = null;
  for (const token of tokens) {
    if (token.map) lastMap = token.map;
    const map = token.map ?? lastMap;
    if (token.type !== "inline" || !map || refs.length >= MAX_NOTE_IMAGES) continue;
    const [firstLine, endLine] = map;
    const blockFrom = starts[firstLine] ?? text.length;
    const blockTo = endLine < starts.length ? starts[endLine] - 1 : text.length;
    const block = text.slice(blockFrom, blockTo);

    const available = new Map<string, number>();
    for (const child of token.children ?? []) {
      const src = child.type === "image" ? child.attrGet?.("src") ?? "" : "";
      if (isRenderableMediaPath(src)) available.set(src, (available.get(src) ?? 0) + 1);
    }
    if (available.size === 0) continue;

    const spans = codeSpanRanges(block);
    EDITABLE_IMAGE_RE.lastIndex = 0;
    for (let m = EDITABLE_IMAGE_RE.exec(block); m; m = EDITABLE_IMAGE_RE.exec(block)) {
      const start = m.index;
      const end = start + m[0].length;
      const path = m[2];
      if (!isRenderableMediaPath(path) || used.has(blockFrom + start)) continue;
      if (spans.some(([a, b]) => start < b && end > a)) continue;
      let slashes = 0;
      while (start - slashes - 1 >= 0 && block[start - slashes - 1] === "\\") slashes += 1;
      if (slashes % 2 === 1) continue;
      const left = available.get(path) ?? 0;
      if (left === 0) continue;
      const parsed = parseImageAlt(m[1]);
      const standalone = parseDrawingPath(path);
      if (standalone && parsed.drawingId && parsed.drawingId !== standalone.drawingId) continue;
      available.set(path, left - 1);
      used.add(blockFrom + start);
      refs.push({
        from: blockFrom + start,
        to: blockFrom + end,
        altFrom: blockFrom + start + 2,
        altTo: blockFrom + start + 2 + m[1].length,
        path,
        alt: parsed.alt,
        width: parsed.width,
        drawingId: standalone ? standalone.drawingId : parsed.drawingId,
        kind: standalone ? "drawing" : "asset",
      });
      if (refs.length >= MAX_NOTE_IMAGES) break;
    }
  }
  return refs.sort((a, b) => a.from - b.from);
}

export function formatMediaMarkdown(ref: Pick<NoteImageRef, "alt" | "width" | "kind" | "path">, drawingId: string | null, slug: string | null): string {
  if (ref.kind === "drawing" && drawingId && slug) {
    return `![${formatImageAlt(ref.alt, ref.width, drawingId)}](${drawingPath(slug, drawingId)})`;
  }
  return `![${formatImageAlt(ref.alt, ref.width, drawingId)}](${ref.path})`;
}

export function referencedDrawingIds(refs: readonly NoteImageRef[]): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const ref of refs) {
    if (ref.drawingId && !seen.has(ref.drawingId)) {
      seen.add(ref.drawingId);
      out.push(ref.drawingId);
    }
  }
  return out;
}
