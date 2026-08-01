// Guards desktop empty-search layout: .no-results must leave the auto-fit
// grid (absolute overlay) so lanes are not compressed. Mobile flex layout is
// unchanged (min-width 621px only).

import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const stylesSource = readFileSync(join(__dirname, "..", "..", "styles.css"), "utf8");
const boardSource = readFileSync(join(__dirname, "board.ts"), "utf8");
const renderingSource = readFileSync(join(__dirname, "board-rendering.ts"), "utf8");

function mediaBlock(css: string, query: string): string | null {
  const start = css.search(new RegExp(`@media\\s*\\(\\s*${query}\\s*\\)\\s*\\{`));
  if (start < 0) return null;
  const open = css.indexOf("{", start);
  let depth = 0;
  for (let i = open; i < css.length; i++) {
    if (css[i] === "{") depth++;
    else if (css[i] === "}") {
      depth--;
      if (depth === 0) return css.slice(open + 1, i);
    }
  }
  return null;
}

function mediaBlockContaining(css: string, query: string, needle: string): string | null {
  const re = new RegExp(`@media\\s*\\(\\s*${query}\\s*\\)\\s*\\{`, "g");
  let match: RegExpExecArray | null;
  while ((match = re.exec(css)) !== null) {
    const open = css.indexOf("{", match.index);
    let depth = 0;
    for (let i = open; i < css.length; i++) {
      if (css[i] === "{") depth++;
      else if (css[i] === "}") {
        depth--;
        if (depth === 0) {
          const body = css.slice(open + 1, i);
          if (body.includes(needle)) return body;
          break;
        }
      }
    }
  }
  return null;
}

describe("board empty-search no-results layout", () => {
  it("overlays .no-results on desktop/tablet without participating in the grid", () => {
    const desktop = mediaBlockContaining(stylesSource, "min-width:\\s*621px", ".board > .no-results");
    expect(desktop).not.toBeNull();
    expect(desktop!).toMatch(/\.board\s*>\s*\.no-results\s*\{[^}]*position:\s*absolute/);
    expect(desktop!).not.toMatch(/\.board\s*>\s*\.no-results\s*\{[^}]*grid-column/);
  });

  it("does not add no-results layout rules inside the mobile board breakpoint", () => {
    const mobile = mediaBlock(stylesSource, "max-width:\\s*620px");
    expect(mobile).not.toBeNull();
    expect(mobile!).not.toMatch(/\.no-results/);
  });

  it("still inserts .no-results as a direct child of .board", () => {
    expect(renderingSource).toMatch(/class="no-results"/);
    expect(boardSource).toMatch(/buildNoResultsHtml\(search\)/);
    expect(boardSource).toMatch(/insertAdjacentHTML\("beforeend", buildNoResultsHtml\(search\)\)/);
  });
});
