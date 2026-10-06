import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import ts from "typescript";

const root = process.cwd();
const targets = [
  join(root, "modules"),
  join(root, "app.js"),
];

const violations = [];
const dialogNames = new Set(["alert", "confirm", "prompt"]);

function isBrowserDialogCall(expression) {
  if (ts.isIdentifier(expression)) return dialogNames.has(expression.text);
  return ts.isPropertyAccessExpression(expression)
    && ts.isIdentifier(expression.expression)
    && expression.expression.text === "window"
    && dialogNames.has(expression.name.text);
}

function scanFile(filePath) {
  const content = readFileSync(filePath, "utf8");
  const source = ts.createSourceFile(filePath, content, ts.ScriptTarget.Latest, true);

  function visit(node) {
    if (ts.isCallExpression(node) && isBrowserDialogCall(node.expression)) {
      const { line } = source.getLineAndCharacterOfPosition(node.expression.getStart(source));
      violations.push(`${filePath}:${line + 1}`);
    }
    ts.forEachChild(node, visit);
  }

  visit(source);
}

function scanPath(pathValue) {
  const stats = statSync(pathValue);
  if (stats.isFile()) {
    scanFile(pathValue);
    return;
  }
  const entries = readdirSync(pathValue, { withFileTypes: true });
  for (const entry of entries) {
    const fullPath = join(pathValue, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "dist" || entry.name === "node_modules") continue;
      scanPath(fullPath);
      continue;
    }
    if (!/\.(ts|js)$/.test(entry.name)) continue;
    if (/\.(?:test|spec)\.(?:ts|js)$/.test(entry.name)) continue;
    scanFile(fullPath);
  }
}

for (const target of targets) {
  scanPath(target);
}

if (violations.length > 0) {
  console.error("Raw browser alert()/confirm()/prompt() calls are disallowed in maintained frontend sources. Use the shared dialog helpers.");
  for (const violation of violations) {
    console.error(` - ${violation}`);
  }
  process.exit(1);
}

console.log("Browser-dialog guard passed.");
