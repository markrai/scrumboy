export function transitionVoiceInteractionState(state, event) {
    if (event === "reset")
        return "idle";
    if (event === "cancel")
        return "cancelled";
    if (event === "error")
        return "error";
    if (event === "success")
        return "success";
    switch (event) {
        case "start_command":
            return "listening_command";
        case "parsed":
            return state === "listening_command" ? "parsed" : state;
        case "show_feedback":
            return state === "parsed" ? "showing_feedback_or_confirmation" : state;
        case "speak_confirmation":
            return state === "showing_feedback_or_confirmation" || state === "listening_confirmation" ? "speaking_confirmation" : state;
        case "listen_confirmation":
            return state === "speaking_confirmation" ? "listening_confirmation" : state;
        case "execute":
            return state === "parsed" || state === "showing_feedback_or_confirmation" || state === "listening_confirmation" ? "executing" : state;
        default:
            return state;
    }
}
