export function canCreateBoards(user) {
    return user?.capabilities?.createBoards ?? user?.systemRole !== "guest";
}
