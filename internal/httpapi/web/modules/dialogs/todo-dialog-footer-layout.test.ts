import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const stylesSource = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "..", "..", "styles.css"),
  "utf8",
);
const todoMarkup = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "..", "..", "index.html"),
  "utf8",
);

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

describe("todo dialog mobile footer buttons", () => {
  it("keeps Save and Delete at a 44px tap target inside the 620px footer rules", () => {
    const mobile = mediaBlockContaining(stylesSource, "max-width:\\s*620px", "#saveTodoBtn");
    expect(mobile).not.toBeNull();
    expect(mobile!).toMatch(
      /#todoDialog\s+\.dialog__footer\s+#saveTodoBtn\s*\{[^}]*min-height:\s*44px/,
    );
    expect(mobile!).toMatch(
      /#todoDialog\s+\.dialog__footer\s+#deleteTodoBtn[\s\S]*#saveTodoBtn\s*\{[^}]*min-height:\s*44px/,
    );
  });

  it("vertically centers the Tags/Links Add label in the fixed-height button", () => {
    expect(stylesSource).toMatch(
      /\.tags-add-btn\s*\{[^}]*align-items:\s*center/,
    );
  });

  it("right-aligns Save in the todo footer when Delete and dates are hidden", () => {
    expect(stylesSource).toMatch(
      /#todoDialog\s+\.dialog__footer\s+#saveTodoBtn\s*\{[^}]*margin-inline-start:\s*auto/,
    );
  });

  it("centers the mobile todo dialog without transform so the title caret can scroll", () => {
    const mobile = mediaBlockContaining(stylesSource, "max-width:\\s*620px", "#todoTitle");
    expect(mobile).not.toBeNull();
    expect(mobile!).toMatch(
      /#todoDialog\.dialog\s*\{[^}]*transform:\s*none/,
    );
    expect(mobile!).toMatch(
      /#todoDialog\s+#todoTitle\s*\{[^}]*touch-action:\s*manipulation/,
    );
  });
  it("keeps Details and References in separate, accessible Todo panels", () => {
    expect(todoMarkup).toContain('id="todoDetailsTab"');
    expect(todoMarkup).toContain('id="todoReferencesTab"');
    expect(todoMarkup).toContain('id="todoDetailsPanel"');
    expect(todoMarkup).toContain('id="todoReferencesPanel"');
    const detailsStart = todoMarkup.indexOf('id="todoDetailsPanel"');
    const refsStart = todoMarkup.indexOf('id="todoReferencesPanel"');
    expect(detailsStart).toBeLessThan(refsStart);
    expect(todoMarkup.indexOf('id="todoExternalResourcesField"')).toBeGreaterThan(refsStart);
    expect(todoMarkup.indexOf('id="todoTitle"')).toBeGreaterThan(detailsStart);
    expect(todoMarkup.indexOf('id="todoTitle"')).toBeLessThan(refsStart);
    expect(stylesSource).toContain('.todo-dialog__panel[hidden]');
    expect(stylesSource).toMatch(/\.todo-dialog__tab\s*\{[^}]*padding:\s*10px 16px/);
  });

});
