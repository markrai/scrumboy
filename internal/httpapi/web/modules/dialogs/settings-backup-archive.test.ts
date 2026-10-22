// @vitest-environment happy-dom
// Backup export/import with image archives: browser naming and the native fail-closed guard.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const showToast = vi.fn();
const apiFetch = vi.fn();
const apiFetchForm = vi.fn();
let inlineImagesEnabled = true;

vi.mock("../utils.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../utils.js")>()),
  showToast: (msg: string) => showToast(msg),
}));
vi.mock("../api.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api.js")>()),
  apiFetch: (...a: unknown[]) => apiFetch(...a),
  apiFetchForm: (...a: unknown[]) => apiFetchForm(...a),
}));
vi.mock("../i18n/index.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../i18n/index.js")>()),
  t: (key: string) => key,
}));
vi.mock("../state/selectors.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../state/selectors.js")>()),
  getInlineImagesEnabled: () => inlineImagesEnabled,
}));

// settings.ts reads #settingsDialog at import time, so modules load after the DOM exists.
document.body.innerHTML = '<dialog id="settingsDialog"></dialog>';
const { installAppRuntime, resetAppRuntimeForTests } = await import("../platform/runtime.js");
const { __handleBackupExportForTest, __handleBackupFileSelectForTest } = await import("./settings.js");

type Response = { ok: boolean; status: number; json(): Promise<any>; blob(): Promise<Blob> };

function install(kind: "browser" | "capacitor", respond: (path: string) => Response) {
  const request = vi.fn(async (path: string) => respond(path));
  installAppRuntime({
    kind,
    capability: () => null,
    assetOrigin: () => "",
    serverOrigin: () => "",
    publicLinkOrigin: () => "",
    supportsPWA: () => false,
    supportsWebPush: () => false,
    supportsInteractiveOIDC: () => false,
    startInteractiveOIDC: async () => undefined,
    transport: () => ({ request, openEventStream: vi.fn(), acquireResource: vi.fn(), logout: vi.fn() }),
  } as any);
  return request;
}

function selectFile(file: File): Event {
  const input = document.createElement("input");
  input.type = "file";
  Object.defineProperty(input, "files", { value: [file] });
  const event = new Event("change");
  Object.defineProperty(event, "target", { value: input });
  return event;
}

describe("backup archives", () => {
  let downloads: string[];

  beforeEach(() => {
    showToast.mockReset();
    apiFetch.mockReset();
    apiFetchForm.mockReset();
    inlineImagesEnabled = true;
    downloads = [];
    document.body.innerHTML = '<dialog id="settingsDialog"></dialog><div id="backupPreview"></div><div id="backupWarnings"></div><button id="backupImportBtn"></button><input type="radio" name="importMode" value="merge" checked>';
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      downloads.push(this.download);
    });
    (window.URL as any).createObjectURL = () => "blob:x";
    (window.URL as any).revokeObjectURL = () => undefined;
  });

  afterEach(() => {
    resetAppRuntimeForTests();
    vi.restoreAllMocks();
  });

  it("names browser exports .sbz for archives and .json otherwise", async () => {
    install("browser", () => ({ ok: true, status: 200, json: async () => ({}), blob: async () => new Blob(["PK\x03\x04rest"], { type: "application/zip" }) }));
    await __handleBackupExportForTest();
    install("browser", () => ({ ok: true, status: 200, json: async () => ({}), blob: async () => new Blob(["{}"], { type: "application/json" }) }));
    await __handleBackupExportForTest();
    expect(downloads[0]).toMatch(/\.sbz$/);
    expect(downloads[1]).toMatch(/\.json$/);
  });

  it("guards native exports and refuses archives without downloading", async () => {
    const request = install("capacitor", () => ({ ok: false, status: 409, json: async () => ({}), blob: async () => new Blob() }));
    await __handleBackupExportForTest();
    expect(request.mock.calls[0][0]).toBe("/api/backup/export?clientMaxBytes=8388608");
    expect(showToast).toHaveBeenCalledWith("settings.backup.native.archiveUnsupported");
    expect(downloads).toHaveLength(0);

    showToast.mockReset();
    install("capacitor", () => ({ ok: false, status: 413, json: async () => ({}), blob: async () => new Blob() }));
    await __handleBackupExportForTest();
    expect(showToast).toHaveBeenCalledWith("settings.backup.native.tooLarge");
  });

  it("never sends the clientMaxBytes guard from a browser", async () => {
    const request = install("browser", () => ({ ok: true, status: 200, json: async () => ({}), blob: async () => new Blob(["{}"]) }));
    await __handleBackupExportForTest();
    expect(request.mock.calls[0][0]).toBe("/api/backup/export");
  });

  it("refuses native archive and oversize imports without reading the file", async () => {
    install("capacitor", () => ({ ok: true, status: 200, json: async () => ({}), blob: async () => new Blob() }));
    const archive = new File(["PK"], "backup.sbz");
    const big = new File(["x"], "big.json");
    Object.defineProperty(big, "size", { value: 9 * 1024 * 1024 });
    for (const f of [archive, big]) {
      const text = vi.spyOn(f, "text");
      const buffer = vi.spyOn(f, "arrayBuffer");
      const slice = vi.spyOn(f, "slice");
      await __handleBackupFileSelectForTest(selectFile(f));
      expect(text).not.toHaveBeenCalled();
      expect(buffer).not.toHaveBeenCalled();
      expect(slice).not.toHaveBeenCalled();
    }
    expect(showToast.mock.calls.map((c) => c[0])).toEqual(["settings.backup.native.archiveUnsupported", "settings.backup.native.tooLarge"]);
    expect(apiFetch).not.toHaveBeenCalled();
    expect(apiFetchForm).not.toHaveBeenCalled();
  });

  it("previews browser archives through a multipart upload", async () => {
    install("browser", () => ({ ok: true, status: 200, json: async () => ({}), blob: async () => new Blob() }));
    apiFetchForm.mockResolvedValue({ projects: 1, todos: 2, tags: 0, assets: 3, assetBytes: 2048 });
    await __handleBackupFileSelectForTest(selectFile(new File(["PK\x03\x04"], "backup.sbz")));
    expect(apiFetchForm).toHaveBeenCalledTimes(1);
    expect(apiFetchForm.mock.calls[0][0]).toBe("/api/backup/preview");
    const form = apiFetchForm.mock.calls[0][1] as FormData;
    expect(form.get("importMode")).toBe("merge");
    expect(form.get("archive")).toBeInstanceOf(File);
    expect(document.getElementById("backupPreview")!.innerHTML).toContain("settings.backup.preview.assets");
  });

  it("rejects archives when inline images are disabled", async () => {
    inlineImagesEnabled = false;
    install("browser", () => ({ ok: true, status: 200, json: async () => ({}), blob: async () => new Blob() }));
    await __handleBackupFileSelectForTest(selectFile(new File(["PK\x03\x04"], "backup.sbz")));
    expect(apiFetchForm).not.toHaveBeenCalled();
    expect(showToast).toHaveBeenCalledWith("settings.backup.toast.archiveUnsupported");
  });

  it("keeps the JSON preview path for .json files", async () => {
    install("browser", () => ({ ok: true, status: 200, json: async () => ({}), blob: async () => new Blob() }));
    apiFetch.mockResolvedValue({ projects: 1, todos: 0, tags: 0 });
    await __handleBackupFileSelectForTest(selectFile(new File([JSON.stringify({ version: "1.2", projects: [] })], "b.json")));
    expect(apiFetch).toHaveBeenCalledWith("/api/backup/preview", expect.objectContaining({ method: "POST" }));
    expect(apiFetchForm).not.toHaveBeenCalled();
  });
});
