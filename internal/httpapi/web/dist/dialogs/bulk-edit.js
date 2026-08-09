import { bulkEditDialog, bulkEditForm } from "../dom/elements.js";
import { apiFetch } from "../api.js";
import { showToast, escapeHTML, isAnonymousBoard, sanitizeHexColor } from "../utils.js";
import { applyFieldTooltips, BULK_EDIT_TOOLTIPS } from "../field-tooltips.js";
import { hasI18nKey, I18N_LOCALE_CHANGED, t } from "../i18n/index.js";
import { getAssigneeFromUrl, getSortFromUrl, getBoard, getSlug, getTag, getSearch, getSprintIdFromUrl, getBoardMembers, } from "../state/selectors.js";
import { invalidateBoard } from "../orchestration/board-refresh.js";
import { setBulkUpdating } from "../realtime/guard.js";
import { boardSprintsEnabled, normalizeSprints } from "../sprints.js";
import { invalidateTagsCache } from "./settings.js";
import { normalizeTagName, resolveColumnKey } from "./todo.js";
const EST_POINTS = new Set([1, 2, 3, 5, 8, 13, 20, 40]);
let bulkTagsList = [];
let bound = false;
function sprintStateLabel(state) {
    const key = `todo.sprint.state.${state}`;
    return state && hasI18nKey(key) ? t(key) : state;
}
function bulkEditHint(count) {
    return count === 1
        ? t("board.bulkEdit.editingSingle")
        : t("board.bulkEdit.editingMultiple", { count });
}
function syncBulkEditDialogCopy(todoCount) {
    const els = getBulkFormEls();
    if (els.title) {
        els.title.textContent = t("board.bulkEdit.title");
    }
    if (els.hint && typeof todoCount === "number" && todoCount > 0) {
        els.hint.textContent = bulkEditHint(todoCount);
    }
}
function findTodoInBoardState(id) {
    const board = getBoard();
    if (!board?.columns)
        return null;
    for (const key of Object.keys(board.columns)) {
        const list = board.columns[key] || [];
        const t = list.find((x) => x.id === id);
        if (t)
            return t;
    }
    return null;
}
function mergeTagsAdditive(existing, toAdd) {
    const map = new Map();
    for (const raw of existing) {
        const t = raw.trim();
        if (!t)
            continue;
        const canon = normalizeTagName(t);
        if (!canon)
            continue;
        map.set(canon.toLowerCase(), canon);
    }
    for (const raw of toAdd) {
        const t = raw.trim();
        if (!t)
            continue;
        const canon = normalizeTagName(t);
        if (!canon)
            continue;
        map.set(canon.toLowerCase(), canon);
    }
    return Array.from(map.values());
}
function renderBulkTagChips() {
    const el = document.getElementById("bulkTagsChips");
    if (!el)
        return;
    const tagColors = getBoard()?.tags || [];
    const colorByName = {};
    tagColors.forEach((t) => {
        if (t.color)
            colorByName[t.name] = t.color;
    });
    el.innerHTML = bulkTagsList
        .map((name) => {
        const c = colorByName[name];
        const safe = sanitizeHexColor(c || null);
        const colorStyle = safe ? `style="border-color: ${safe}; background: ${safe}20; color: ${safe};"` : "";
        return `<span class="tag-chip" data-tag="${escapeHTML(name)}" ${colorStyle}>${escapeHTML(name)}<button type="button" class="tag-chip-remove" data-remove-bulk-tag="${escapeHTML(name)}" aria-label="${escapeHTML(t("board.bulkEdit.removeTag"))}">×</button></span>`;
    })
        .join("");
    el.querySelectorAll("[data-remove-bulk-tag]").forEach((btn) => {
        btn.addEventListener("click", (e) => {
            e.stopPropagation();
            const n = btn.getAttribute("data-remove-bulk-tag") || "";
            bulkTagsList = bulkTagsList.filter((x) => x !== n);
            renderBulkTagChips();
        });
    });
}
function getBulkFormEls() {
    return {
        hint: document.getElementById("bulkEditHint"),
        title: document.getElementById("bulkEditDialogTitle"),
        assignRow: document.getElementById("bulkEditAssigneeRow"),
        sprintRow: document.getElementById("bulkEditSprintRow"),
        tagsRow: document.getElementById("bulkEditTagsRow"),
        estRow: document.getElementById("bulkEditEstimationRow"),
        applyAssign: document.getElementById("bulkApplyAssignee"),
        applySprint: document.getElementById("bulkApplySprint"),
        applyStatus: document.getElementById("bulkApplyStatus"),
        applyTags: document.getElementById("bulkApplyTags"),
        applyEst: document.getElementById("bulkApplyEstimation"),
        assignee: document.getElementById("bulkAssignee"),
        sprint: document.getElementById("bulkSprint"),
        status: document.getElementById("bulkStatus"),
        est: document.getElementById("bulkEstimation"),
        tagsInput: document.getElementById("bulkTagsInput"),
        saveBtn: document.getElementById("saveBulkEditBtn"),
    };
}
function updateApplyButtonState() {
    const els = getBulkFormEls();
    const saveBtn = els.saveBtn;
    if (!saveBtn)
        return;
    const any = !!(els.applyAssign?.checked ||
        els.applySprint?.checked ||
        els.applyStatus?.checked ||
        els.applyTags?.checked ||
        els.applyEst?.checked);
    saveBtn.disabled = !any;
}
function populateStatusSelect(select) {
    const board = getBoard();
    const order = board?.columnOrder;
    if (!select)
        return "";
    if (!order || order.length === 0) {
        select.innerHTML = `<option value="backlog">${escapeHTML(t("board.status.backlog"))}</option>`;
        return "backlog";
    }
    select.innerHTML = order.map((c) => `<option value="${escapeHTML(c.key)}">${escapeHTML(c.name)}</option>`).join("");
    return order[0].key;
}
function populateAssigneeSelect(sel) {
    if (!sel)
        return;
    const members = getBoardMembers();
    const cur = sel.value;
    sel.innerHTML = `<option value="">${escapeHTML(t("todo.assignee.unassigned"))}</option>`;
    members.forEach((m) => {
        const opt = document.createElement("option");
        opt.value = String(m.userId);
        opt.textContent = m.name || m.email || `User ${m.userId}`;
        sel.appendChild(opt);
    });
    if (cur && sel.querySelector(`option[value="${cur}"]`))
        sel.value = cur;
}
async function populateSprintSelect(sel) {
    if (!sel)
        return;
    const slug = getSlug();
    if (!slug)
        return;
    try {
        const res = await apiFetch(`/api/board/${slug}/sprints`);
        const sprints = normalizeSprints(res);
        const cur = sel.value;
        sel.innerHTML = '<option value="">—</option>';
        for (const sp of sprints) {
            const opt = document.createElement("option");
            opt.value = String(sp.id);
            opt.textContent = `${sp.name} (${sprintStateLabel(sp.state)})`;
            sel.appendChild(opt);
        }
        if (cur && sel.querySelector(`option[value="${cur}"]`))
            sel.value = cur;
    }
    catch {
        sel.innerHTML = '<option value="">—</option>';
    }
}
function setPermissionsVisibility(role) {
    const board = getBoard();
    const anonymous = isAnonymousBoard(board);
    const isMaintainer = role === "maintainer" || anonymous;
    const els = getBulkFormEls();
    if (els.assignRow)
        els.assignRow.style.display = !anonymous ? "" : "none";
    if (els.sprintRow)
        els.sprintRow.style.display = !anonymous && role === "maintainer" && boardSprintsEnabled(board) ? "" : "none";
    if (els.tagsRow)
        els.tagsRow.style.display = isMaintainer ? "" : "none";
    if (els.estRow)
        els.estRow.style.display = isMaintainer ? "" : "none";
    if (els.assignRow) {
        const dis = !isMaintainer || anonymous;
        if (els.applyAssign)
            els.applyAssign.disabled = dis;
        if (els.assignee)
            els.assignee.disabled = dis;
    }
    if (els.sprintRow) {
        const dis = role !== "maintainer" || anonymous;
        if (els.applySprint)
            els.applySprint.disabled = dis;
        if (els.sprint)
            els.sprint.disabled = dis;
    }
    if (els.tagsRow) {
        const dis = !isMaintainer;
        if (els.applyTags)
            els.applyTags.disabled = dis;
        if (els.tagsInput)
            els.tagsInput.disabled = dis;
        const addBtn = document.getElementById("bulkAddTagBtn");
        if (addBtn)
            addBtn.disabled = dis;
    }
    if (els.estRow) {
        const dis = !isMaintainer;
        if (els.applyEst)
            els.applyEst.disabled = dis;
        if (els.est)
            els.est.disabled = dis;
    }
}
export async function openBulkEditDialog(initialIds, opts) {
    const kept = [];
    for (const id of initialIds) {
        const t = findTodoInBoardState(id);
        if (t) {
            kept.push(id);
        }
    }
    opts.onPruned(kept);
    if (kept.length < 2) {
        showToast(t("board.bulkEdit.noTodosSelected"));
        return;
    }
    syncBulkEditDialogCopy(kept.length);
    const els = getBulkFormEls();
    [els.applyAssign, els.applySprint, els.applyStatus, els.applyTags, els.applyEst].forEach((c) => {
        if (c)
            c.checked = false;
    });
    bulkTagsList = [];
    renderBulkTagChips();
    if (els.tagsInput)
        els.tagsInput.value = "";
    if (els.est)
        els.est.value = "";
    populateStatusSelect(els.status);
    populateAssigneeSelect(els.assignee);
    if (boardSprintsEnabled(getBoard())) {
        await populateSprintSelect(els.sprint);
    }
    setPermissionsVisibility(opts.role);
    updateApplyButtonState();
    setBulkEditPendingIds(kept, opts.onPruned);
    const dlg = bulkEditDialog;
    if (dlg && typeof dlg.showModal === "function") {
        dlg.showModal();
    }
}
async function runBulkApply(todoIds) {
    const els = getBulkFormEls();
    const applyAssign = !!els.applyAssign?.checked;
    const applySprint = boardSprintsEnabled(getBoard()) && !!els.applySprint?.checked;
    const applyStatus = !!els.applyStatus?.checked;
    const applyTags = !!els.applyTags?.checked;
    const applyEst = !!els.applyEst?.checked;
    const needsPatch = applyAssign || applySprint || applyTags || applyEst;
    const targetColumnKey = els.status?.value || "";
    const assigneeVal = els.assignee && els.assignee.value !== "" ? Number(els.assignee.value) : null;
    const sprintRaw = els.sprint?.value ?? "";
    const sprintIdVal = sprintRaw === "" ? null : Number(sprintRaw);
    const estRaw = els.est?.value ?? "";
    const estParsed = estRaw === "" ? null : Number.parseInt(estRaw, 10);
    const hasValidEst = estParsed != null && EST_POINTS.has(estParsed);
    let success = 0;
    let failed = 0;
    let anyTagsAdded = false;
    const slug = getSlug();
    if (!slug)
        return;
    const scrollY = window.scrollY;
    const scrollX = window.scrollX;
    setBulkUpdating(true);
    try {
        const form = bulkEditForm;
        const onPruned = form ? form.__bulkOnPruned : null;
        const keptIds = [];
        for (const id of todoIds) {
            if (findTodoInBoardState(id))
                keptIds.push(id);
        }
        onPruned?.(keptIds);
        setBulkEditPendingIds(keptIds, onPruned || undefined);
        if (keptIds.length === 0) {
            showToast(t("board.bulkEdit.noTodosSelected"));
            return;
        }
        for (const id of keptIds) {
            const todo = findTodoInBoardState(id);
            if (!todo) {
                failed++;
                continue;
            }
            const currentKey = resolveColumnKey(todo.columnKey || todo.status);
            const needsMove = applyStatus && targetColumnKey !== "" && resolveColumnKey(targetColumnKey) !== currentKey;
            if (!needsPatch && !needsMove) {
                continue;
            }
            try {
                // Hard rule: never send PATCH unless at least one PATCH-scoped checkbox is on.
                if (needsPatch) {
                    const tagsOut = applyTags
                        ? mergeTagsAdditive(todo.tags || [], bulkTagsList)
                        : [...(todo.tags || [])];
                    if (applyTags)
                        anyTagsAdded = true;
                    const patch = {
                        title: todo.title,
                        body: todo.body ?? "",
                        tags: tagsOut,
                        assigneeUserId: applyAssign ? assigneeVal : (todo.assigneeUserId ?? null),
                    };
                    if (applyEst) {
                        patch.estimationPoints = hasValidEst ? estParsed : null;
                    }
                    else {
                        patch.estimationPoints = todo.estimationPoints ?? null;
                    }
                    if (applySprint) {
                        patch.sprintId = sprintIdVal;
                    }
                    await apiFetch(`/api/board/${slug}/todos/${todo.localId}`, {
                        method: "PATCH",
                        body: JSON.stringify(patch),
                    });
                }
                if (needsMove) {
                    await apiFetch(`/api/board/${slug}/todos/${todo.localId}/move`, {
                        method: "POST",
                        body: JSON.stringify({ toColumnKey: targetColumnKey, afterId: null, beforeId: null }),
                    });
                }
                success++;
            }
            catch {
                failed++;
            }
        }
    }
    finally {
        setBulkUpdating(false);
    }
    if (anyTagsAdded) {
        invalidateTagsCache();
    }
    await invalidateBoard(slug, getTag(), getSearch(), getSprintIdFromUrl(), getAssigneeFromUrl(), getSortFromUrl());
    requestAnimationFrame(() => {
        requestAnimationFrame(() => {
            window.scrollTo(scrollX, scrollY);
        });
    });
    const total = success + failed;
    if (total === 0) {
        showToast(t("board.bulkEdit.nothingToUpdate"));
    }
    else if (failed === 0) {
        showToast(success === 1
            ? t("board.bulkEdit.updatedSingle")
            : t("board.bulkEdit.updatedMultiple", { count: success }));
    }
    else {
        showToast(t("board.bulkEdit.updatedPartial", { success, total, failed }));
    }
}
export function initBulkEditDialog(onSuccess) {
    if (bound)
        return;
    bound = true;
    applyFieldTooltips(BULK_EDIT_TOOLTIPS, bulkEditForm ?? document);
    const form = bulkEditForm;
    const closeBtn = document.getElementById("closeBulkEditBtn");
    const cancelBtn = document.getElementById("cancelBulkEditBtn");
    const addTagBtn = document.getElementById("bulkAddTagBtn");
    const checkboxes = [
        "bulkApplyAssignee",
        "bulkApplySprint",
        "bulkApplyStatus",
        "bulkApplyTags",
        "bulkApplyEstimation",
    ];
    checkboxes.forEach((id) => {
        document.getElementById(id)?.addEventListener("change", updateApplyButtonState);
    });
    document.addEventListener(I18N_LOCALE_CHANGED, () => {
        const ids = form?.__bulkTodoIds;
        syncBulkEditDialogCopy(ids?.length);
        renderBulkTagChips();
    });
    function addBulkTagFromInput() {
        const input = document.getElementById("bulkTagsInput");
        if (!input)
            return;
        const raw = input.value.trim();
        if (!raw)
            return;
        const name = normalizeTagName(raw);
        if (!name)
            return;
        const lower = name.toLowerCase();
        if (!bulkTagsList.some((t) => t.toLowerCase() === lower)) {
            bulkTagsList.push(name);
        }
        input.value = "";
        renderBulkTagChips();
    }
    addTagBtn?.addEventListener("click", (e) => {
        e.preventDefault();
        addBulkTagFromInput();
    });
    document.getElementById("bulkTagsInput")?.addEventListener("keydown", (e) => {
        if (e.key === "Enter") {
            e.preventDefault();
            addBulkTagFromInput();
        }
    });
    form?.addEventListener("submit", async (e) => {
        e.preventDefault();
        const ids = form.__bulkTodoIds;
        if (!ids || ids.length === 0)
            return;
        await runBulkApply(ids);
        bulkEditDialog?.close();
        onSuccess();
    });
    const close = () => bulkEditDialog?.close();
    closeBtn?.addEventListener("click", close);
    cancelBtn?.addEventListener("click", close);
}
/** Store ids and optional prune callback on form (showModal + right before apply loop). */
export function setBulkEditPendingIds(ids, onPruned) {
    const form = bulkEditForm;
    if (form) {
        form.__bulkTodoIds = ids;
        form.__bulkOnPruned = onPruned ?? null;
    }
}
