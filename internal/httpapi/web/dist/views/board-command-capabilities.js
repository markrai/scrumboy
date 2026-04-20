const VOICE_COMMAND_ROLES = new Set(["maintainer", "contributor", "viewer"]);
function hasDurableProjectScope(input) {
    return typeof input.projectId === "number"
        && Number.isFinite(input.projectId)
        && input.projectId > 0
        && typeof input.projectSlug === "string"
        && input.projectSlug.trim().length > 0
        && !input.isTemporary
        && !input.isAnonymous;
}
export function canShowVoiceCommands(input) {
    return hasDurableProjectScope(input)
        && VOICE_COMMAND_ROLES.has(String(input.role || "").toLowerCase());
}
export function canRunVoiceMutationCommands(input) {
    return hasDurableProjectScope(input)
        && String(input.role || "").toLowerCase() === "maintainer";
}
