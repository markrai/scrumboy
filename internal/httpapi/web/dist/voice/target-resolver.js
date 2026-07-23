import { normalizeTitleReference } from './normalize.js';
import { localizedCommandFailure, isCommandFailure } from './schema.js';
const MIN_CANDIDATE_SCORE = 70;
const SINGLE_CANDIDATE_AUTO_SCORE = 75;
const CLEAR_WIN_SCORE_GAP = 12;
function boardTodos(board) {
    return Object.values(board.columns ?? {}).flat();
}
function findTodoInBoard(board, localId) {
    for (const todo of boardTodos(board)) {
        if (todo.localId === localId)
            return todo;
    }
    return null;
}
async function resolveTodoByLocalId(localId, context) {
    const fromBoard = findTodoInBoard(context.board, localId);
    if (fromBoard)
        return { ok: true, value: fromBoard };
    if (!context.callTool) {
        return localizedCommandFailure("unknown_story", "voice.errors.todoNotFound", "Todo #{localId} was not found in this project.", { localId });
    }
    try {
        const data = await context.callTool("todos_get", {
            projectSlug: context.projectSlug,
            localId,
        });
        if (data?.todo?.localId === localId) {
            return { ok: true, value: data.todo };
        }
        return localizedCommandFailure("unknown_story", "voice.errors.todoNotFound", "Todo #{localId} was not found in this project.", { localId });
    }
    catch {
        return localizedCommandFailure("unknown_story", "voice.errors.todoNotFound", "Todo #{localId} was not found in this project.", { localId });
    }
}
function mergeCandidates(existing, candidates) {
    for (const candidate of candidates) {
        if (!Number.isInteger(candidate.localId) || candidate.localId <= 0 || !candidate.title)
            continue;
        if (!existing.has(candidate.localId)) {
            existing.set(candidate.localId, candidate);
        }
    }
}
function localTitleCandidates(board) {
    return boardTodos(board).map((todo) => ({
        localId: todo.localId,
        title: todo.title,
    }));
}
async function remoteTitleCandidates(phrase, context) {
    if (!context.callTool)
        return [];
    try {
        const data = await context.callTool("todos_search", {
            projectSlug: context.projectSlug,
            query: phrase,
            limit: 10,
        });
        if (!Array.isArray(data?.items))
            return [];
        return data.items
            .filter((item) => !item.projectSlug || item.projectSlug === context.projectSlug)
            .map((item) => ({
            localId: Number(item.localId),
            title: String(item.title ?? ""),
        }))
            .filter((item) => Number.isInteger(item.localId) && item.localId > 0 && item.title.trim().length > 0);
    }
    catch {
        return [];
    }
}
function orderedTokensContained(queryTokens, titleTokens) {
    let titleIndex = 0;
    for (const token of queryTokens) {
        const nextIndex = titleTokens.indexOf(token, titleIndex);
        if (nextIndex < 0)
            return false;
        titleIndex = nextIndex + 1;
    }
    return true;
}
function scoreTitleMatch(query, title) {
    const normalizedTitle = normalizeTitleReference(title);
    if (!query || !normalizedTitle)
        return 0;
    if (normalizedTitle === query)
        return 100;
    if (normalizedTitle.startsWith(`${query} `))
        return 90;
    const queryTokens = query.split(" ").filter(Boolean);
    const titleTokens = normalizedTitle.split(" ").filter(Boolean);
    if (queryTokens.length === 0 || titleTokens.length === 0)
        return 0;
    if (queryTokens.length === 1 && titleTokens.includes(queryTokens[0]))
        return 76;
    const allTokensContained = queryTokens.every((token) => titleTokens.includes(token));
    if (allTokensContained && orderedTokensContained(queryTokens, titleTokens))
        return 82;
    if (allTokensContained)
        return 78;
    if (query.length >= 4 && normalizedTitle.includes(query))
        return 75;
    return 0;
}
export function rankTitleCandidates(phrase, candidates) {
    const query = normalizeTitleReference(phrase);
    return candidates
        .map((candidate) => ({
        ...candidate,
        score: scoreTitleMatch(query, candidate.title),
    }))
        .filter((candidate) => candidate.score >= MIN_CANDIDATE_SCORE)
        .sort((a, b) => {
        if (b.score !== a.score)
            return b.score - a.score;
        return a.localId - b.localId;
    });
}
async function resolveTodoByTitle(target, context) {
    const candidates = new Map();
    mergeCandidates(candidates, localTitleCandidates(context.board));
    mergeCandidates(candidates, await remoteTitleCandidates(target.phrase, context));
    const ranked = rankTitleCandidates(target.phrase, Array.from(candidates.values()));
    if (ranked.length === 0) {
        return localizedCommandFailure("unknown_story", "voice.errors.noStrongTitleMatch", "No strong todo title match was found in this project.");
    }
    const exactMatches = ranked.filter((candidate) => candidate.score === 100);
    if (exactMatches.length === 1) {
        const resolved = await resolveTodoByLocalId(exactMatches[0].localId, context);
        if (isCommandFailure(resolved))
            return resolved;
        return { ok: true, value: { todo: resolved.value } };
    }
    if (exactMatches.length > 1) {
        return localizedCommandFailure("ambiguous_story", "voice.errors.todoAmbiguous", "More than one todo matched. Choose one.", {}, {
            candidates: exactMatches.slice(0, 3).map(({ localId, title }) => ({ localId, title })),
        });
    }
    const [first, second] = ranked;
    const hasClearSingle = !second && first.score >= SINGLE_CANDIDATE_AUTO_SCORE;
    const hasClearWinner = !!second && first.score >= SINGLE_CANDIDATE_AUTO_SCORE && first.score - second.score >= CLEAR_WIN_SCORE_GAP;
    if (hasClearSingle || hasClearWinner) {
        const resolved = await resolveTodoByLocalId(first.localId, context);
        if (isCommandFailure(resolved))
            return resolved;
        return { ok: true, value: { todo: resolved.value } };
    }
    return localizedCommandFailure("ambiguous_story", "voice.errors.todoAmbiguous", "More than one todo matched. Choose one.", {}, {
        candidates: ranked.slice(0, 3).map(({ localId, title }) => ({ localId, title })),
    });
}
export async function resolveTodoTarget(target, context, selectedLocalId, allowedLocalIds) {
    if (selectedLocalId != null) {
        if (!allowedLocalIds || !allowedLocalIds.includes(selectedLocalId)) {
            return localizedCommandFailure("invalid_schema", "voice.errors.selectedTodoNotOffered", "Selected todo was not one of the offered choices.");
        }
        const resolved = await resolveTodoByLocalId(selectedLocalId, context);
        if (isCommandFailure(resolved))
            return resolved;
        return { ok: true, value: { todo: resolved.value, ambiguousId: target.kind === "id" ? target.ambiguousId : false } };
    }
    if (target.kind === "id") {
        const resolved = await resolveTodoByLocalId(target.localId, context);
        if (isCommandFailure(resolved))
            return resolved;
        return { ok: true, value: { todo: resolved.value, ambiguousId: target.ambiguousId } };
    }
    return resolveTodoByTitle(target, context);
}
