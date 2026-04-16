import { fetchProjectMembers } from '../members-cache.js';
import { getAuthStatusAvailable, getUser } from '../state/selectors.js';
import { setBoardLaneMeta, setBoardMembers, setProjectId, setSearch, setSlug, setTag, } from '../state/mutations.js';
import { isAnonymousBoard } from '../utils.js';
import { getBoardColumns } from './board-rendering.js';
function laneMetaKeyCandidates(key) {
    const lower = key.toLowerCase();
    const upper = key.toUpperCase();
    const out = [key, lower, upper];
    if (lower === "doing" || upper === "IN_PROGRESS")
        out.push("IN_PROGRESS", "doing");
    return Array.from(new Set(out));
}
function buildLaneMetaFromBoard(board) {
    const rawMeta = (board?.columnsMeta ?? {});
    const keys = new Set();
    getBoardColumns(board).forEach((c) => keys.add(c.key));
    Object.keys(board?.columns ?? {}).forEach((k) => keys.add(k));
    Object.keys(rawMeta).forEach((k) => keys.add(k));
    const out = {};
    keys.forEach((key) => {
        let source;
        for (const candidate of laneMetaKeyCandidates(key)) {
            if (rawMeta[candidate] != null) {
                source = rawMeta[candidate];
                break;
            }
        }
        out[key] = {
            hasMore: source?.hasMore === true,
            nextCursor: source?.nextCursor ?? null,
            loading: false,
            totalCount: source?.totalCount,
        };
    });
    return out;
}
export async function bootstrapLoadedBoardView(args) {
    const { board, slug, tag, search } = args;
    const projectId = board?.project?.id;
    if (!projectId) {
        throw new Error("Invalid board response");
    }
    setSlug(slug);
    setProjectId(projectId);
    setTag(tag || "");
    setSearch(search || "");
    setBoardLaneMeta(buildLaneMetaFromBoard(board));
    const user = getUser();
    if (user && projectId && !isAnonymousBoard(board)) {
        try {
            const members = await fetchProjectMembers(projectId);
            if (!args.isCurrent())
                return false;
            setBoardMembers(members);
            const currentMember = members.find((m) => m.userId === user.id);
            args.setResolvedRole(currentMember ? currentMember.role : null);
            args.markMembersFetched(projectId);
        }
        catch {
            if (!args.isCurrent())
                return false;
            setBoardMembers([]);
            args.setResolvedRole(null);
        }
    }
    else {
        args.setResolvedRole(null);
    }
    const showBackToProjects = !!getAuthStatusAvailable();
    const minimalTopbar = !!board?.project?.expiresAt && (!showBackToProjects || getUser() == null);
    args.renderLoadedBoard({
        projectId,
        backLabel: "← Projects",
        backHref: showBackToProjects && getUser() != null ? "/" : "",
        minimalTopbar,
    });
    args.markLoadSuccess(slug);
    return true;
}
