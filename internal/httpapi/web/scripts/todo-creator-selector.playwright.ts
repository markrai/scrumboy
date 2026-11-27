import { execFileSync, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { DatabaseSync } from "node:sqlite";
import { expect, test, type Browser, type BrowserContext, type Page } from "@playwright/test";

const webDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(webDir, "..", "..", "..");
const OWNER = { email: "creator-owner@example.test", password: "password123", name: "Creator Owner" };
const SECOND = { email: "creator-second@example.test", password: "password123", name: "Second Maintainer" };
const CONTRIBUTOR = { email: "creator-contributor@example.test", password: "password123", name: "Assigned Contributor" };
const VIEWER = { email: "creator-viewer@example.test", password: "password123", name: "Project Viewer" };
const OUTSIDER = {
  email: "creator-outsider-with-an-intentionally-long-address-for-layout-qualification@example.test",
  password: "password123",
  name: "Outside Creator With An Intentionally Long Display Name For Responsive Qualification",
};

type Account = typeof OWNER;
type UserRecord = { id: number; email: string; name?: string };
type ProjectRecord = { id: number; slug: string };
type TodoRecord = { localId: number; createdByUserId?: number | null; assigneeUserId?: number | null; title?: string };
type Cookie = { name: string; value: string; path?: string; httpOnly?: boolean };

let server: ChildProcessWithoutNullStreams | null = null;
let tempRoot = "";
let dataDir = "";
let baseUrl = "";
let normalProject: ProjectRecord;
let temporaryProject: ProjectRecord;
let revocationProject: ProjectRecord;
let ownerClient: ReturnType<typeof apiClient>;
let secondClient: ReturnType<typeof apiClient>;
let contributorClient: ReturnType<typeof apiClient>;
let viewerClient: ReturnType<typeof apiClient>;
let outsiderClient: ReturnType<typeof apiClient>;
let ownerUser: UserRecord;
let secondUser: UserRecord;
let contributorUser: UserRecord;
let viewerUser: UserRecord;
let outsiderUser: UserRecord;
const todos = new Map<string, TodoRecord>();

test.describe.configure({ mode: "serial" });

test.beforeAll(async () => {
  test.setTimeout(300_000);
  tempRoot = await fs.promises.mkdtemp(path.join(os.tmpdir(), "scrumboy-todo-creator-"));
  dataDir = path.join(tempRoot, "data");
  await fs.promises.mkdir(dataDir, { recursive: true });
  const binary = path.join(tempRoot, process.platform === "win32" ? "scrumboy.exe" : "scrumboy");
  execFileSync("go", ["build", "-buildvcs=false", "-o", binary, "./cmd/scrumboy"], { cwd: repoRoot, stdio: "inherit" });
  const port = await freePort();
  baseUrl = `http://127.0.0.1:${port}`;
  server = spawn(binary, [], {
    cwd: repoRoot,
    env: {
      ...process.env,
      SCRUMBOY_MODE: "full",
      DATA_DIR: dataDir,
      BIND_ADDR: `127.0.0.1:${port}`,
      SCRUMBOY_TLS_CERT: path.join(dataDir, "no-cert"),
      SCRUMBOY_TLS_KEY: path.join(dataDir, "no-key"),
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stderr = "";
  server.stderr.on("data", (chunk) => { stderr += String(chunk); });
  server.stdout.on("data", () => {});
  try {
    await waitForServer(baseUrl, 120_000);
  } catch (error) {
    throw new Error(`server failed to start: ${error}\n${stderr}`);
  }

  ownerClient = apiClient(baseUrl);
  await ownerClient.json("POST", "/api/auth/bootstrap", OWNER);
  secondUser = await ownerClient.json<UserRecord>("POST", "/api/admin/users", SECOND);
  contributorUser = await ownerClient.json<UserRecord>("POST", "/api/admin/users", CONTRIBUTOR);
  viewerUser = await ownerClient.json<UserRecord>("POST", "/api/admin/users", VIEWER);
  outsiderUser = await ownerClient.json<UserRecord>("POST", "/api/admin/users", OUTSIDER);
  for (let index = 1; index <= 10; index += 1) {
    await ownerClient.json("POST", "/api/admin/users", {
      email: `creator-candidate-${index}@example.test`,
      password: "password123",
      name: `Candidate ${String(index).padStart(2, "0")}`,
    });
  }
  const users = await ownerClient.json<UserRecord[]>("GET", "/api/admin/users");
  ownerUser = users.find((user) => user.email === OWNER.email)!;

  secondClient = await loggedInClient(SECOND);
  contributorClient = await loggedInClient(CONTRIBUTOR);
  viewerClient = await loggedInClient(VIEWER);
  outsiderClient = await loggedInClient(OUTSIDER);

  normalProject = await ownerClient.json<ProjectRecord>("POST", "/api/projects", { name: "Creator Qualification Board" });
  await addMember(normalProject, secondUser.id, "maintainer");
  await addMember(normalProject, contributorUser.id, "contributor");
  await addMember(normalProject, viewerUser.id, "viewer");

  for (const title of ["Pipeline story", "Reset story", "Null story", "Nonmember story", "Assigned story", "Failure story", "Keyboard story", "Cancel story", "Layout story", "Archived story", "Late first", "Late second"]) {
    todos.set(title, await ownerClient.json<TodoRecord>("POST", `/api/board/${normalProject.slug}/todos`, { title }));
  }
  await ownerClient.json("POST", `/api/board/${normalProject.slug}/todos/archive`, { localIds: [todo("Archived story").localId] });

  temporaryProject = await ownerClient.json<ProjectRecord>("POST", "/api/projects", { name: "Authenticated Temporary Board" });
  todos.set("Temporary story", await ownerClient.json<TodoRecord>("POST", `/api/board/${temporaryProject.slug}/todos`, { title: "Temporary story" }));

  revocationProject = await ownerClient.json<ProjectRecord>("POST", "/api/projects", { name: "Revocation Board" });
  await addMember(revocationProject, secondUser.id, "maintainer");
  todos.set("Revocation story", await ownerClient.json<TodoRecord>("POST", `/api/board/${revocationProject.slug}/todos`, { title: "Revocation story" }));

  const database = new DatabaseSync(path.join(dataDir, "app.db"));
  try {
    database.exec("PRAGMA busy_timeout = 5000");
    database.prepare("UPDATE todos SET created_by_user_id = NULL WHERE project_id = ? AND local_id = ?").run(normalProject.id, todo("Null story").localId);
    database.prepare("UPDATE todos SET created_by_user_id = ? WHERE project_id = ? AND local_id = ?").run(outsiderUser.id, normalProject.id, todo("Nonmember story").localId);
    database.prepare("UPDATE todos SET assignee_user_id = ? WHERE project_id = ? AND local_id = ?").run(contributorUser.id, normalProject.id, todo("Assigned story").localId);
    database.prepare("UPDATE projects SET expires_at = ? WHERE id = ?").run(Date.now() + 86_400_000, temporaryProject.id);
  } finally {
    database.close();
  }
});

test.afterAll(async () => {
  if (server && !server.killed) {
    server.kill("SIGTERM");
    await new Promise<void>((resolve) => {
      const timeout = setTimeout(() => {
        try { server?.kill("SIGKILL"); } catch {}
        resolve();
      }, 10_000);
      server?.once("exit", () => {
        clearTimeout(timeout);
        resolve();
      });
    });
  }
  if (tempRoot) await fs.promises.rm(tempRoot, { recursive: true, force: true }).catch(() => {});
});

test("Maintainer selection is deferred and one canonical save PATCH persists it without changing assignment or membership", async ({ page }) => {
  await authenticate(page.context(), ownerClient);
  const patches: Record<string, unknown>[] = [];
  page.on("request", (request) => {
    if (request.method() === "PATCH" && request.url().endsWith(`/todos/${todo("Pipeline story").localId}`)) {
      patches.push(request.postDataJSON());
    }
  });
  await openStory(page, normalProject, "Pipeline story");
  await expectComputedDisplay(page, "#todoCreatorEditBtn", false);
  await page.locator("#todoCreatorEditBtn").click();
  await expect(page.locator(`[data-user-id="${outsiderUser.id}"]`)).toBeVisible();
  await page.locator(`[data-user-id="${outsiderUser.id}"]`).click();
  expect(patches).toHaveLength(0);
  await expect(page.locator("#todoDialogCreatedBy")).toContainText(OUTSIDER.name);
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  await expect.poll(() => patches.length).toBe(1);
  expect(patches[0].createdByUserId).toBe(outsiderUser.id);
  const persisted = await readTodo(normalProject, todo("Pipeline story").localId);
  expect(persisted.createdByUserId).toBe(outsiderUser.id);
  expect(persisted.assigneeUserId ?? null).toBeNull();
  const members = await ownerClient.json<UserRecord[]>("GET", `/api/projects/${normalProject.id}/members`);
  expect(members.some((member) => member.id === outsiderUser.id || (member as any).userId === outsiderUser.id)).toBe(false);
});

test("unchanged and reset attribution are omitted, including reset to an original null creator", async ({ page }) => {
  await authenticate(page.context(), ownerClient);
  const captured = new Map<number, Record<string, unknown>[]>([
    [todo("Reset story").localId, []],
    [todo("Null story").localId, []],
  ]);
  page.on("request", (request) => {
    if (request.method() !== "PATCH") return;
    for (const [localId, payloads] of captured) {
      if (request.url().endsWith(`/todos/${localId}`)) payloads.push(request.postDataJSON());
    }
  });

  await openStory(page, normalProject, "Reset story");
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  await expect.poll(() => captured.get(todo("Reset story").localId)!.length).toBe(1);
  expect(captured.get(todo("Reset story").localId)![0]).not.toHaveProperty("createdByUserId");

  await openStory(page, normalProject, "Reset story");
  await page.locator("#todoCreatorEditBtn").click();
  await page.locator(`[data-user-id="${viewerUser.id}"]`).click();
  await expect(page.locator("#todoCreatorResetBtn")).toBeVisible();
  await page.locator("#todoTitle").fill("Reset story with another pending edit");
  await page.locator("#todoCreatorResetBtn").focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("#todoCreatorResetBtn")).toBeHidden();
  await page.locator("#todoTitle").fill("Reset story");
  await page.locator("#saveTodoBtn").click();
  await expect.poll(() => captured.get(todo("Reset story").localId)!.length).toBe(2);
  expect(captured.get(todo("Reset story").localId)![1]).not.toHaveProperty("createdByUserId");

  await openStory(page, normalProject, "Null story");
  await expect(page.locator("#todoDialogCreatedBy")).toHaveText("Creator not recorded");
  await page.locator("#todoCreatorEditBtn").click();
  await page.locator(`[data-user-id="${viewerUser.id}"]`).click();
  await page.locator("#todoCreatorResetBtn").click();
  await page.locator("#saveTodoBtn").click();
  await expect.poll(() => captured.get(todo("Null story").localId)!.length).toBe(1);
  expect(captured.get(todo("Null story").localId)![0]).not.toHaveProperty("createdByUserId");
  expect((await readTodo(normalProject, todo("Null story").localId)).createdByUserId ?? null).toBeNull();
});

test("null and unresolved non-member attribution resolve without silently replacing identity", async ({ page }) => {
  await authenticate(page.context(), ownerClient);
  await openStory(page, normalProject, "Nonmember story");
  await expect(page.locator("#todoDialogCreatedBy")).toHaveText("Creator details unavailable");
  await page.locator("#todoCreatorEditBtn").click();
  await expect(page.locator("#todoDialogCreatedBy")).toContainText(OUTSIDER.name);
  await expect(page.locator(`[data-user-id="${outsiderUser.id}"]`)).toContainText("Not a project member");
  await page.locator("#todoCreatorSelectorClose").click();
  await page.locator("#closeTodoBtn").click();

  await openStory(page, normalProject, "Null story");
  await page.locator("#todoCreatorEditBtn").click();
  await page.locator(`[data-user-id="${outsiderUser.id}"]`).click();
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();
  expect((await readTodo(normalProject, todo("Null story").localId)).createdByUserId).toBe(outsiderUser.id);
});

test("failed save and discard keep pending UI state but never persist the discarded creator", async ({ page }) => {
  await authenticate(page.context(), ownerClient);
  const failurePath = `/api/board/${normalProject.slug}/todos/${todo("Failure story").localId}`;
  await page.route(`**${failurePath}`, async (route) => {
    if (route.request().method() === "PATCH") {
      await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: { message: "forced failure" } }) });
    } else {
      await route.continue();
    }
  });
  await openStory(page, normalProject, "Failure story");
  await page.locator("#todoCreatorEditBtn").click();
  await page.locator(`[data-user-id="${viewerUser.id}"]`).click();
  await page.locator("#saveTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeVisible();
  await expect(page.locator("#todoDialogCreatedBy")).toContainText(VIEWER.name);
  expect((await readTodo(normalProject, todo("Failure story").localId)).createdByUserId).toBe(ownerUser.id);
  await page.unroute(`**${failurePath}`);
  await page.locator("#closeTodoBtn").click();
  await page.locator("#confirmDialogConfirm").click();
  await expect(page.locator("#todoDialog")).toBeHidden();

  await openStory(page, normalProject, "Cancel story");
  await page.locator("#todoCreatorEditBtn").click();
  await page.locator(`[data-user-id="${viewerUser.id}"]`).click();
  await page.locator("#closeTodoBtn").click();
  await page.locator("#confirmDialogConfirm").click();
  expect((await readTodo(normalProject, todo("Cancel story").localId)).createdByUserId).toBe(ownerUser.id);
});

test("real form keyboard events prevent Enter submission, respect composition, dismiss on Escape, and keep End visible", async ({ page }) => {
  await authenticate(page.context(), ownerClient);
  const mutations: string[] = [];
  page.on("request", (request) => {
    if (request.method() === "PATCH" && request.url().endsWith(`/todos/${todo("Keyboard story").localId}`)) mutations.push(request.url());
  });
  await openStory(page, normalProject, "Keyboard story");
  await page.locator("#todoCreatorEditBtn").click();
  const search = page.locator("#todoCreatorSearch");
  await search.fill("no-user-has-this-value");
  await search.press("Enter");
  await expect(page.locator("#todoCreatorSelector")).toBeVisible();
  await expect(page.locator("#todoDialog")).toBeVisible();
  expect(mutations).toHaveLength(0);

  await search.fill(VIEWER.name);
  await search.evaluate((element) => element.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true })));
  await search.press("Enter");
  await expect(page.locator("#todoCreatorSelector")).toBeVisible();
  await expect(page.locator("#todoDialogCreatedBy")).toContainText(OWNER.name);
  expect(mutations).toHaveLength(0);
  await search.evaluate((element) => element.dispatchEvent(new CompositionEvent("compositionend", { bubbles: true })));
  await search.press("Escape");
  await expect(page.locator("#todoCreatorSelector")).toBeHidden();
  await expect(page.locator("#todoDialog")).toBeVisible();

  await page.locator("#todoCreatorEditBtn").click();
  await search.fill("");
  await search.press("End");
  const activeId = await search.getAttribute("aria-activedescendant");
  expect(activeId).toBeTruthy();
  const activeBox = await page.locator(`#${activeId}`).boundingBox();
  const optionsBox = await page.locator("#todoCreatorOptions").boundingBox();
  expect(activeBox).not.toBeNull();
  expect(optionsBox).not.toBeNull();
  expect(activeBox!.y).toBeGreaterThanOrEqual(optionsBox!.y - 1);
  expect(activeBox!.y + activeBox!.height).toBeLessThanOrEqual(optionsBox!.y + optionsBox!.height + 1);
  await page.locator("#todoCreatorSelectorClose").focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("#todoCreatorSelector")).toBeHidden();
  expect(mutations).toHaveLength(0);
});

test("computed visibility follows actual membership and story state without unauthorized candidate requests", async ({ browser }) => {
  await visibilityCase(browser, viewerClient, normalProject, "Reset story", false, OWNER.name);
  await visibilityCase(browser, contributorClient, normalProject, "Reset story", false, OWNER.name);
  await visibilityCase(browser, contributorClient, normalProject, "Assigned story", false, OWNER.name);
  await visibilityCase(browser, outsiderClient, temporaryProject, "Temporary story", false);
  await visibilityCase(browser, ownerClient, normalProject, "Pipeline story", true);

  const ownerContext = await authenticatedContext(browser, ownerClient);
  const ownerPage = await ownerContext.newPage();
  await ownerPage.goto(`${baseUrl}/${normalProject.slug}`);
  await ownerPage.locator("#newTodoBtn").click();
  await expect(ownerPage.locator("#todoDialog")).toBeVisible();
  await expectComputedDisplay(ownerPage, "#todoCreatorEditBtn", true);
  await ownerPage.locator("#closeTodoBtn").click();
  await ownerPage.goto(`${baseUrl}/${normalProject.slug}/archive`);
  await ownerPage.locator(`[data-archive-open="${todo("Archived story").localId}"]`).click();
  await expect(ownerPage.locator("#todoDialog")).toBeVisible();
  await expectComputedDisplay(ownerPage, "#todoCreatorEditBtn", true);
  await expect(ownerPage.locator("#todoDialogCreatedBy")).toContainText(OWNER.name);
  await ownerContext.close();

  const anonymousContext = await browser.newContext();
  const anonymousPage = await anonymousContext.newPage();
  const anonymousRequests: string[] = [];
  anonymousPage.on("request", (request) => {
    if (request.url().includes("/available-users")) anonymousRequests.push(request.url());
  });
  await anonymousPage.goto(`${baseUrl}/anon`);
  await expect(anonymousPage.locator("#newTodoBtn")).toBeVisible({ timeout: 30_000 });
  await anonymousPage.locator("#newTodoBtn").click();
  await anonymousPage.locator("#todoTitle").fill("Anonymous creator visibility");
  await anonymousPage.locator("#saveTodoBtn").click();
  await expect(anonymousPage.locator(".card[data-todo-local-id]", { hasText: "Anonymous creator visibility" })).toBeVisible();
  await anonymousPage.locator(".card[data-todo-local-id]", { hasText: "Anonymous creator visibility" }).click();
  await expectComputedDisplay(anonymousPage, "#todoCreatorEditBtn", true);
  expect(anonymousRequests).toHaveLength(0);
  await anonymousContext.close();
});

test("authorization revocation and a late response clear privileged candidates across dialog lifecycles", async ({ page }) => {
  await authenticate(page.context(), secondClient);
  let continueRevoked!: () => void;
  const revokedGate = new Promise<void>((resolve) => { continueRevoked = resolve; });
  let revokedStarted!: () => void;
  const revokedRequest = new Promise<void>((resolve) => { revokedStarted = resolve; });
  const revokedPattern = `**/api/projects/${revocationProject.id}/available-users`;
  await page.route(revokedPattern, async (route) => {
    revokedStarted();
    await revokedGate;
    await route.continue().catch(() => {});
  });
  await openStory(page, revocationProject, "Revocation story");
  await page.locator("#todoCreatorEditBtn").click();
  await revokedRequest;
  await ownerClient.json("PATCH", `/api/projects/${revocationProject.id}/members/${secondUser.id}`, { role: "viewer" });
  const revokedMembers = await ownerClient.json<Array<{ userId: number; role: string }>>("GET", `/api/projects/${revocationProject.id}/members`);
  expect(revokedMembers.find((member) => member.userId === secondUser.id)?.role).toBe("viewer");
  await setFrontendMemberRole(page, secondUser.id, "viewer");
  continueRevoked();
  await expectComputedDisplay(page, "#todoCreatorEditBtn", true);
  await expect(page.locator("#todoCreatorOptions")).toBeEmpty();
  await expect(page.locator("#todoCreatorSearch")).toHaveValue("");
  await page.unroute(revokedPattern);

  const revokedRequests: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes(`/api/projects/${revocationProject.id}/available-users`)) revokedRequests.push(request.url());
  });
  await openStory(page, revocationProject, "Revocation story");
  await expectComputedDisplay(page, "#todoCreatorEditBtn", true);
  await expect(page.locator("#todoDialogCreatedBy")).toContainText(OWNER.name);
  expect(revokedRequests).toHaveLength(0);
  await page.locator("#closeTodoBtn").click();

  await authenticate(page.context(), ownerClient);
  await page.goto(`${baseUrl}/${normalProject.slug}`);
  let releaseLate!: () => void;
  const lateGate = new Promise<void>((resolve) => { releaseLate = resolve; });
  let lateStarted!: () => void;
  const lateRequest = new Promise<void>((resolve) => { lateStarted = resolve; });
  const normalPattern = `**/api/projects/${normalProject.id}/available-users`;
  await page.route(normalPattern, async (route) => {
    lateStarted();
    await lateGate;
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify([{ id: 999999, name: "Stale Candidate", email: "stale@example.test" }]) }).catch(() => {});
  });
  await page.locator(".card[data-todo-local-id]", { hasText: "Late first" }).click();
  await page.locator("#todoCreatorEditBtn").click();
  await lateRequest;
  await page.locator("#todoCreatorSelectorClose").click();
  await page.locator("#closeTodoBtn").click();
  await page.locator(".card[data-todo-local-id]", { hasText: "Late second" }).click();
  releaseLate();
  await expect(page.locator('[data-user-id="999999"]')).toHaveCount(0);
  await expect(page.locator("#todoDialogCreatedBy")).toContainText(OWNER.name);
  await page.unroute(normalPattern);
});

test("membership refresh clears loaded candidates and pending creator mutations without dirtying unrelated fields", async ({ page }) => {
  await ownerClient.json("PATCH", `/api/projects/${revocationProject.id}/members/${secondUser.id}`, { role: "maintainer" });
  await authenticate(page.context(), secondClient);
  await openStory(page, revocationProject, "Revocation story");
  await page.locator("#todoCreatorEditBtn").click();
  await expect(page.locator(`[data-user-id="${outsiderUser.id}"]`)).toBeVisible();

  await ownerClient.json("PATCH", `/api/projects/${revocationProject.id}/members/${secondUser.id}`, { role: "viewer" });
  await setFrontendMemberRole(page, secondUser.id, "viewer");
  await expectComputedDisplay(page, "#todoCreatorEditBtn", true);
  await expect(page.locator("#todoCreatorSelector")).toBeHidden();
  await expect(page.locator("#todoCreatorOptions")).toBeEmpty();
  await expect(page.locator("#todoCreatorSearch")).toHaveValue("");
  await expect(page.locator("#todoDialogCreatedBy")).toContainText(OWNER.name);
  await expect(page.locator("body")).not.toContainText(OUTSIDER.email);
  await page.locator("#closeTodoBtn").click();
  await expect(page.locator("#todoDialog")).toBeHidden();

  await ownerClient.json("PATCH", `/api/projects/${revocationProject.id}/members/${secondUser.id}`, { role: "maintainer" });
  await openStory(page, revocationProject, "Revocation story");
  await page.locator("#todoCreatorEditBtn").click();
  await page.locator(`[data-user-id="${viewerUser.id}"]`).click();
  await page.locator("#todoTitle").fill("Revocation story pending title");
  await fillTodoBody(page, "Pending body survives creator invalidation");

  const patches: Record<string, unknown>[] = [];
  page.on("request", (request) => {
    if (request.method() === "PATCH" && request.url().endsWith(`/todos/${todo("Revocation story").localId}`)) {
      patches.push(request.postDataJSON());
    }
  });
  await ownerClient.json("PATCH", `/api/projects/${revocationProject.id}/members/${secondUser.id}`, { role: "viewer" });
  await setFrontendMemberRole(page, secondUser.id, "viewer");

  await expect(page.locator("#todoDialogCreatedBy")).toContainText(OWNER.name);
  await expect(page.locator("#todoTitle")).toHaveValue("Revocation story pending title");
  await expectTodoBody(page, "Pending body survives creator invalidation");
  await page.locator("#saveTodoBtn").click();
  await expect.poll(() => patches.length).toBe(1);
  expect(patches[0]).not.toHaveProperty("createdByUserId");
  await expect(page.locator("#todoTitle")).toHaveValue("Revocation story pending title");
  await expectTodoBody(page, "Pending body survives creator invalidation");
});

test("responsive, theme, RTL, pseudo, German, and accessibility matrix remains usable", async ({ page }) => {
  await authenticate(page.context(), ownerClient);
  await page.setViewportSize({ width: 1440, height: 900 });
  await openStory(page, normalProject, "Layout story");
  const cases = [
    { name: "desktop dark", width: 1440, height: 900, locale: "en", light: false },
    { name: "desktop light pseudo", width: 1440, height: 900, locale: "pseudo", light: true },
    { name: "mobile dark RTL", width: 390, height: 844, locale: "ar", light: false },
    { name: "mobile light", width: 390, height: 844, locale: "en", light: true },
    { name: "narrow German", width: 320, height: 640, locale: "de", light: false },
    { name: "short landscape", width: 640, height: 320, locale: "en", light: true },
    { name: "reduced viewport", width: 390, height: 280, locale: "en", light: false },
  ];
  for (const entry of cases) {
    await test.step(entry.name, async () => {
      await page.setViewportSize({ width: entry.width, height: entry.height });
      await page.evaluate(async ({ locale, light }) => {
        document.documentElement.setAttribute("data-theme", light ? "light" : "");
        if (locale === "pseudo") await window.scrumboyI18nQa?.enablePseudo();
        else {
          const i18n = await import("/dist/i18n/index.js");
          await i18n.setLocale(locale);
        }
      }, { locale: entry.locale, light: entry.light });
      if (await page.locator("#todoCreatorSelector").isVisible()) await page.locator("#todoCreatorSelectorClose").click();
      await page.locator("#todoCreatorEditBtn").click();
      await expect(page.locator("#todoCreatorSelector")).toBeVisible();
      await expect(page.locator("#todoCreatorSearch")).toBeFocused();
      await expect(page.locator("#todoCreatorSelector")).toHaveAttribute("role", "dialog");
      await expect(page.locator("#todoCreatorSearch")).toHaveAttribute("role", "combobox");
      await expect(page.locator("#todoCreatorSelector")).toHaveAccessibleName(/.+/);
      await expect(page.locator("#todoCreatorSearch")).toHaveAccessibleName(/.+/);
      await expect(page.locator("#todoCreatorSelectorClose")).toHaveAccessibleName(/.+/);
      const measurements = await page.evaluate(() => {
        const selector = document.getElementById("todoCreatorSelector")!;
        const options = document.getElementById("todoCreatorOptions")!;
        const close = document.getElementById("todoCreatorSelectorClose")!;
        const edit = document.getElementById("todoCreatorEditBtn")!;
        const option = options.querySelector<HTMLElement>("[role=option]");
        const box = selector.getBoundingClientRect();
        const viewport = window.visualViewport;
        const left = viewport?.offsetLeft ?? 0;
        const top = viewport?.offsetTop ?? 0;
        const width = viewport?.width ?? window.innerWidth;
        const height = viewport?.height ?? window.innerHeight;
        return {
          box: { left: box.left, top: box.top, right: box.right, bottom: box.bottom },
          viewport: { left, top, right: left + width, bottom: top + height },
          docOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth,
          optionOverflow: options.scrollWidth > options.clientWidth,
          closeSize: close.getBoundingClientRect().height,
          editSize: edit.getBoundingClientRect().height,
          optionSize: option?.getBoundingClientRect().height ?? 0,
          direction: document.documentElement.dir,
        };
      });
      expect(measurements.box.left).toBeGreaterThanOrEqual(measurements.viewport.left - 1);
      expect(measurements.box.right).toBeLessThanOrEqual(measurements.viewport.right + 1);
      expect(measurements.box.top).toBeGreaterThanOrEqual(measurements.viewport.top - 1);
      expect(measurements.box.bottom).toBeLessThanOrEqual(measurements.viewport.bottom + 1);
      expect(measurements.docOverflow).toBe(false);
      expect(measurements.optionOverflow).toBe(false);
      if (entry.width <= 767) {
        expect(measurements.closeSize).toBeGreaterThanOrEqual(44);
        expect(measurements.editSize).toBeGreaterThanOrEqual(44);
        expect(measurements.optionSize).toBeGreaterThanOrEqual(44);
      }
      if (entry.locale === "ar") expect(measurements.direction).toBe("rtl");
      if (entry.locale === "pseudo") await expect(page.locator("#todoCreatorSelector")).toContainText("[!!");
      await page.locator("#todoCreatorSelectorClose").click();
    });
  }

  await page.setViewportSize({ width: 390, height: 280 });
  await page.locator("#closeTodoBtn").click();
  await page.locator("#newTodoBtn").click();
  await expect(page.locator("#saveTodoBtn")).toBeVisible();
  const newTodoLayout = await dialogFooterMeasurements(page);
  expect(newTodoLayout.footerInsideDialog).toBe(true);
  expect(newTodoLayout.saveInsideFooter).toBe(true);
  expect(newTodoLayout.horizontalOverflow).toBe(false);
  await page.locator("#closeTodoBtn").click();

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${baseUrl}/${normalProject.slug}/archive`);
  await page.locator(`[data-archive-open="${todo("Archived story").localId}"]`).click();
  await expect(page.locator("#todoArchiveBanner")).toBeVisible();
  await expect(page.locator("#todoTitle")).toHaveAttribute("readonly", "");
  await expect(page.locator("#saveTodoBtn")).toBeHidden();
  const archivedLayout = await dialogFooterMeasurements(page);
  expect(archivedLayout.footerInsideDialog).toBe(true);
  expect(archivedLayout.horizontalOverflow).toBe(false);
});

function todo(title: string): TodoRecord {
  const record = todos.get(title);
  if (!record) throw new Error(`missing todo fixture: ${title}`);
  return record;
}

async function addMember(project: ProjectRecord, userId: number, role: string): Promise<void> {
  await ownerClient.json("POST", `/api/projects/${project.id}/members`, { user_id: userId, role });
}

async function loggedInClient(account: Account) {
  const client = apiClient(baseUrl);
  await client.json("POST", "/api/auth/login", { email: account.email, password: account.password });
  return client;
}

async function readTodo(project: ProjectRecord, localId: number): Promise<TodoRecord> {
  return ownerClient.json<TodoRecord>("GET", `/api/board/${project.slug}/todos/${localId}`);
}

async function authenticate(context: BrowserContext, client: ReturnType<typeof apiClient>): Promise<void> {
  await context.addCookies(client.cookies().map((cookie) => ({
    name: cookie.name,
    value: cookie.value,
    domain: "127.0.0.1",
    path: cookie.path || "/",
    httpOnly: cookie.httpOnly,
    secure: false,
    sameSite: "Lax" as const,
  })));
}

async function authenticatedContext(browser: Browser, client: ReturnType<typeof apiClient>): Promise<BrowserContext> {
  const context = await browser.newContext();
  await authenticate(context, client);
  return context;
}

async function openStory(page: Page, project: ProjectRecord, title: string): Promise<void> {
  await page.goto(`${baseUrl}/${project.slug}`);
  await page.locator(".card[data-todo-local-id]", { hasText: title }).first().click();
  await expect(page.locator("#todoDialog")).toBeVisible();
}

async function expectComputedDisplay(page: Page, selector: string, hidden: boolean): Promise<void> {
  await expect.poll(() => page.locator(selector).evaluate((element) => getComputedStyle(element).display === "none")).toBe(hidden);
}

async function dialogFooterMeasurements(page: Page): Promise<{
  footerInsideDialog: boolean;
  saveInsideFooter: boolean;
  horizontalOverflow: boolean;
}> {
  return page.evaluate(() => {
    const dialog = document.getElementById("todoDialog")!;
    const footer = dialog.querySelector<HTMLElement>(".dialog__footer")!;
    const save = document.getElementById("saveTodoBtn")!;
    const dialogBox = dialog.getBoundingClientRect();
    const footerBox = footer.getBoundingClientRect();
    const saveBox = save.getBoundingClientRect();
    const saveVisible = getComputedStyle(save).display !== "none";
    return {
      footerInsideDialog: footerBox.left >= dialogBox.left - 1 && footerBox.right <= dialogBox.right + 1 && footerBox.bottom <= dialogBox.bottom + 1,
      saveInsideFooter: !saveVisible || saveBox.left >= footerBox.left - 1 && saveBox.right <= footerBox.right + 1 && saveBox.bottom <= footerBox.bottom + 1,
      horizontalOverflow: dialog.scrollWidth > dialog.clientWidth || footer.scrollWidth > footer.clientWidth,
    };
  });
}

async function visibilityCase(
  browser: Browser,
  client: ReturnType<typeof apiClient>,
  project: ProjectRecord,
  title: string,
  visible: boolean,
  expectedAttribution?: string,
): Promise<void> {
  const context = await authenticatedContext(browser, client);
  const page = await context.newPage();
  const availableRequests: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/available-users")) availableRequests.push(request.url());
  });
  await openStory(page, project, title);
  await expectComputedDisplay(page, "#todoCreatorEditBtn", !visible);
  if (expectedAttribution) {
    await expect(page.locator("#todoDialogCreatedBy")).toContainText(expectedAttribution);
  }
  if (visible) {
    await page.locator("#todoCreatorEditBtn").click();
    await expect.poll(() => availableRequests.length).toBe(1);
  } else {
    expect(availableRequests).toHaveLength(0);
    if (expectedAttribution) {
      await page.evaluate(async () => {
        const i18n = await import("/dist/i18n/index.js");
        await i18n.setLocale("pseudo");
      });
      await expect(page.locator("#todoDialogCreatedBy")).toContainText(expectedAttribution);
      expect(availableRequests).toHaveLength(0);
    }
  }
  await context.close();
}

async function setFrontendMemberRole(page: Page, userId: number, role: string): Promise<void> {
  await page.evaluate(async ({ userId, role }) => {
    const selectors = await import("/dist/state/selectors.js");
    const mutations = await import("/dist/state/mutations.js");
    const creatorSelector = await import("/dist/dialogs/todo-creator-selector.js");
    mutations.setBoardMembers(selectors.getBoardMembers().map((member) =>
      Number(member.userId) === userId ? { ...member, role } : member,
    ));
    creatorSelector.refreshTodoCreatorSelectorAuthorization();
  }, { userId, role });
}

async function fillTodoBody(page: Page, value: string): Promise<void> {
  const visualEditor = page.locator("#todoBodyEditor .cm-content");
  if (await visualEditor.isVisible()) {
    await visualEditor.fill(value);
    return;
  }
  await page.locator("#todoBody").fill(value);
}

async function expectTodoBody(page: Page, value: string): Promise<void> {
  await expect.poll(() => page.evaluate(async () => {
    const todoDialog = await import("/dist/dialogs/todo.js");
    return todoDialog.getTodoNotesValue();
  })).toBe(value);
}

async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const socket = net.createServer();
    socket.listen(0, "127.0.0.1", () => {
      const address = socket.address();
      if (!address || typeof address === "string") {
        socket.close();
        reject(new Error("failed to allocate port"));
        return;
      }
      socket.close((error) => error ? reject(error) : resolve(address.port));
    });
  });
}

async function waitForServer(url: string, timeoutMs: number): Promise<void> {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    try {
      const response = await fetch(`${url}/api/auth/status`, { headers: { "X-Scrumboy": "1" } });
      if (response.ok) return;
    } catch {}
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`timeout waiting for ${url}`);
}

function apiClient(url: string) {
  const jar = new Map<string, Cookie>();
  function storeSetCookie(header: string | null): void {
    if (!header) return;
    for (const part of header.split(/,(?=\s*[^;=]+=[^;]+)/)) {
      const [nameValue, ...attributes] = part.split(";").map((value) => value.trim());
      const equals = nameValue.indexOf("=");
      if (equals < 0) continue;
      const cookie: Cookie = { name: nameValue.slice(0, equals), value: nameValue.slice(equals + 1), path: "/", httpOnly: false };
      for (const attribute of attributes) {
        const lower = attribute.toLowerCase();
        if (lower.startsWith("path=")) cookie.path = attribute.slice(5);
        if (lower === "httponly") cookie.httpOnly = true;
      }
      jar.set(cookie.name, cookie);
    }
  }
  const cookieHeader = () => [...jar.values()].map((cookie) => `${cookie.name}=${cookie.value}`).join("; ");
  return {
    cookies: () => [...jar.values()],
    async json<T = unknown>(method: string, pathname: string, body?: unknown): Promise<T> {
      const response = await fetch(`${url}${pathname}`, {
        method,
        headers: { "Content-Type": "application/json", "X-Scrumboy": "1", Cookie: cookieHeader() },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const headers = response.headers as Headers & { getSetCookie?: () => string[] };
      if (typeof headers.getSetCookie === "function") {
        for (const value of headers.getSetCookie()) storeSetCookie(value);
      } else {
        storeSetCookie(response.headers.get("set-cookie"));
      }
      if (response.status === 204) return null as T;
      const data = await response.json().catch(() => null);
      if (!response.ok) throw new Error(`${method} ${pathname} -> ${response.status} ${JSON.stringify(data)}`);
      return data as T;
    },
  };
}
