export const SUPPORTED_LOCALES = ["en", "zh", "hi", "es", "ar", "fr", "bn", "pt", "id", "ur", "ru", "de", "ja", "sw", "vi", "tr", "ko", "fa", "th", "it", "ms", "pl", "uk", "pseudo"];
export const PUBLIC_LOCALES = ["en", "zh", "hi", "es", "ar", "fr", "bn", "pt", "id", "ur", "ru", "de", "ja", "sw", "vi", "tr", "ko", "fa", "th", "it", "ms", "pl", "uk"];
export const LOCALE_STORAGE_KEY = "scrumboy.locale";
export const I18N_LOCALE_CHANGED = "scrumboy:i18n-locale-changed";
const LOCALE_COOKIE_MAX_AGE_SECONDS = 60 * 60 * 24 * 365;
export const LOCALE_LABELS = {
    en: "English",
    de: "Deutsch",
    it: "Italiano",
    ms: "Bahasa Melayu",
    pl: "Polski",
    uk: "Українська",
    fr: "Français",
    bn: "বাংলা",
    pt: "Português (Brasil)",
    es: "Español (Latinoamérica)",
    ar: "العربية",
    ru: "Русский",
    ja: "日本語",
    sw: "Kiswahili",
    tr: "Türkçe",
    ko: "한국어",
    zh: "简体中文",
    id: "Bahasa Indonesia",
    vi: "Tiếng Việt",
    th: "ไทย",
    ur: "اردو",
    fa: "فارسی",
    hi: "हिन्दी",
    pseudo: "Pseudo",
};
export const PUBLIC_LOCALE_FLAG_PATHS = {
    en: "/assets/flags/us.svg",
    de: "/assets/flags/de.svg",
    it: "/assets/flags/it.svg",
    ms: "/assets/flags/my.svg",
    pl: "/assets/flags/pl.svg",
    uk: "/assets/flags/ua.svg",
    fr: "/assets/flags/fr.svg",
    bn: "/assets/flags/bd.svg",
    pt: "/assets/flags/br.svg",
    es: "/assets/flags/mx.svg",
    ar: "/assets/flags/sa.svg",
    ru: "/assets/flags/ru.svg",
    ja: "/assets/flags/jp.svg",
    sw: "/assets/flags/tz.svg",
    tr: "/assets/flags/tr.svg",
    ko: "/assets/flags/kr.svg",
    zh: "/assets/flags/cn.svg",
    id: "/assets/flags/id.svg",
    vi: "/assets/flags/vn.svg",
    th: "/assets/flags/th.svg",
    ur: "/assets/flags/pk.svg",
    fa: "/assets/flags/ir.svg",
    hi: "/assets/flags/in.svg",
};
const BOOTSTRAP_EN_CATALOG = {
    "common.add": "Add",
    "common.apply": "Apply",
    "common.cancel": "Cancel",
    "common.close": "Close",
    "common.confirm": "Confirm",
    "common.delete": "Delete",
    "common.prompt": "Prompt",
    "common.remove": "Remove",
    "common.save": "Save",
    "common.value": "Value",
    "auth.2fa.accountFallback": "your account",
    "auth.2fa.failed": "Verification failed.",
    "auth.2fa.helper": "Enter the 6-digit code from your authenticator app, or a recovery code.",
    "auth.2fa.placeholder": "Code for {account}",
    "auth.2fa.submit": "Verify",
    "auth.2fa.title": "Two-factor authentication",
    "auth.actions.bootstrap": "Bootstrap",
    "auth.actions.login": "Login",
    "auth.actions.resetPassword": "Reset Password",
    "auth.bootstrap.failed": "Setup failed.",
    "auth.bootstrap.title": "First-time setup",
    "auth.fields.confirmPassword.label": "Confirm password",
    "auth.fields.confirmPassword.placeholder": "Confirm new password",
    "auth.fields.email.placeholder": "Email",
    "auth.fields.name.placeholder": "Name",
    "auth.fields.newPassword.label": "New password",
    "auth.fields.newPassword.placeholder": "Min 8 characters",
    "auth.fields.password.placeholder": "Password",
    "auth.forgot.backToSignIn": "Back to sign in",
    "auth.forgot.failed": "Could not request a password reset.",
    "auth.forgot.helper": "Enter your email. If an account exists for that email address, we’ll send a password reset link that expires in 30 minutes.",
    "auth.forgot.link": "Forgot password?",
    "auth.forgot.submit": "Send reset link",
    "auth.forgot.success": "If an account exists for that email address, a password reset email has been sent.",
    "auth.forgot.title": "Reset your password",
    "auth.login.failed": "Login failed.",
    "auth.oidc.button": "Continue with SSO",
    "auth.oidc.error.email": "A verified email address is required.",
    "auth.oidc.error.generic": "Authentication failed.",
    "auth.oidc.error.provider": "The identity provider returned an error.",
    "auth.oidc.error.state_invalid": "Login session expired or invalid. Please try again.",
    "auth.oidc.error.token": "Authentication failed. Please try again.",
    "auth.password.hide": "Hide password",
    "auth.password.show": "Show password",
    "auth.reset.helper": "Enter your new password. The link expires in 30 minutes.",
    "auth.reset.invalidLink": "Invalid or missing reset link",
    "auth.reset.invalidOrExpiredToken": "Invalid or expired reset token",
    "auth.reset.passwordsMismatch": "Passwords do not match",
    "auth.reset.success": "Password reset successfully. Please log in.",
    "auth.reset.title": "Reset Password",
    "auth.shared.helper": "Authentication is enabled for this instance. Anonymous boards remain shareable by URL; durable projects require sign-in.",
    "auth.shared.or": "or",
    "auth.signIn.title": "Sign in",
    "board.actions.changeProjectImage": "Change project image",
    "board.actions.clearSearch": "Clear search",
    "board.actions.deleteProject": "Delete project",
    "board.actions.manageMembers": "Members",
    "board.actions.newTodo": "New Todo",
    "board.actions.openWall": "Open wall",
    "board.actions.renameProject": "Rename",
    "board.actions.settings": "Settings",
    "board.backToProjects": "\u2190 Projects",
    "board.bulkEdit.editingMultiple": "Editing {count} todos.",
    "board.bulkEdit.editingSingle": "Editing 1 todo.",
    "board.bulkEdit.noTodosSelected": "No todos on the board to edit.",
    "board.bulkEdit.nothingToUpdate": "Nothing to update.",
    "board.bulkEdit.removeTag": "Remove tag",
    "board.bulkEdit.title": "Bulk edit",
    "board.bulkEdit.updatedMultiple": "Updated {count} todos",
    "board.bulkEdit.updatedPartial": "Updated {success} of {total} todos ({failed} failed)",
    "board.bulkEdit.updatedSingle": "Updated 1 todo",
    "board.filters.all": "All",
    "board.filters.label": "Tags:",
    "board.filters.next": "Next tags",
    "board.filters.previous": "Previous tags",
    "board.filters.scheduled": "Scheduled",
    "board.filters.unscheduled": "Unscheduled",
    "board.loadMore": "Load more",
    "board.loadMoreFailed": "Failed to load more",
    "board.members.addFailed": "Failed to add member",
    "board.members.addMember": "Add Member",
    "board.members.addNewMember": "Add New Member",
    "board.members.added": "Member added successfully",
    "board.members.allUsersAreMembers": "All users are already members",
    "board.members.close": "Close",
    "board.members.currentMembers": "Current Members",
    "board.members.demoteAction": "Demote",
    "board.members.demoteConfirm": "Demote {name} to {role}?",
    "board.members.demoteTitle": "Demote member?",
    "board.members.dialogTitle": "Manage Members",
    "board.members.dialogTitleReadOnly": "Members",
    "board.members.loadFailed": "Failed to load members",
    "board.members.noMembers": "No members yet",
    "board.members.projectLabel": "Project: {name}",
    "board.members.remove": "Remove",
    "board.members.removeConfirm": "Remove {name} from this project?",
    "board.members.removeFailed": "Failed to remove member",
    "board.members.removeFromProject": "Remove from project",
    "board.members.removeTitle": "Remove member?",
    "board.members.removed": "Member removed from project",
    "board.members.role": "Role",
    "board.members.role.contributor": "Contributor",
    "board.members.role.maintainer": "Maintainer",
    "board.members.role.viewer": "Viewer",
    "board.members.roleUpdated": "Role updated",
    "board.members.selectUser": "Select a user...",
    "board.members.thisMember": "this member",
    "board.members.updateRoleFailed": "Failed to update role",
    "board.members.user": "User",
    "board.noResults": "No todos found matching \"{search}\"",
    "board.openTodo.accessDenied": "You don't have access to this todo",
    "board.openTodo.failed": "Failed to load todo",
    "board.openTodo.notFound": "Todo not found",
    "board.project.imageUpdated": "Project image updated",
    "board.project.imageUploadFailed": "Upload failed",
    "board.project.deleteFailed": "Failed to delete project",
    "board.project.nameLabel": "Project Name",
    "board.project.namePlaceholder": "Project name",
    "board.project.renamed": "Project renamed",
    "board.project.renameAction": "Rename",
    "board.project.renameFailed": "Failed to rename project",
    "board.project.renameTitle": "Rename Project",
    "board.refreshFailed": "Failed to refresh board",
    "board.search.placeholder.desktop": "Search todos...",
    "board.search.placeholder.mobile": "Search",
    "board.selection.multiple": "Edit {count} selected",
    "board.selection.single": "Edit 1 selected",
    "board.status.backlog": "Backlog",
    "board.todo.dragToReorder": "Drag to reorder",
    "board.todo.moveFailed": "Failed to move todo",
    "board.todo.movedTo": "Todo moved to {lane}",
    "board.voice.boardChanged": "The board changed before commands opened",
    "board.voice.loadFailed": "Commands failed to load",
    "board.voice.unavailable": "Commands are unavailable for this board",
    "board.wallOpenFailed": "Could not open the wall",
    "dashboard.empty.assignedTodos": "No todos assigned to you.",
    "dashboard.loadMore.action": "Load more",
    "dashboard.loadMore.loading": "Loading...",
    "dashboard.loadMore.loadingAria": "Loading more",
    "dashboard.loading.assignedTodos": "Loading assigned todos...",
    "dashboard.project.openTitle": "Open {name}",
    "dashboard.sort.activity": "Activity",
    "dashboard.sort.board.long": "Board Order (per project)",
    "dashboard.sort.board.short": "Board Order",
    "dashboard.sort.hint": "Order matches each project's board: column, then drag order. Projects appear in a fixed order (not alphabetical or by activity).",
    "dashboard.sort.label": "Sort",
    "dashboard.sprint.unscheduled": "Unscheduled",
    "dashboard.stats.assigned": "ASSIGNED",
    "dashboard.stats.avgLeadTime": "Avg. lead time",
    "dashboard.stats.currentSprint": "CURRENT SPRINT",
    "dashboard.stats.inProgress": "In progress",
    "dashboard.stats.leadTimeValue": "{days}d",
    "dashboard.stats.oldestInProgress": "Oldest in progress",
    "dashboard.stats.oldestWipValue": "#{localId} {title} — {ageDays}d ({projectName})",
    "dashboard.stats.pointsOnly": "{points} pts",
    "dashboard.stats.pointsTodos": "{points} pts · {todos} todos",
    "dashboard.stats.storiesPoints": "Stories: {stories}% · Points: {points}%",
    "dashboard.stats.teamCompletion": "TEAM COMPLETION",
    "dashboard.stats.testing": "Testing",
    "dashboard.stats.throughputLast4Weeks": "Throughput (last 4 weeks)",
    "dashboard.stats.totalAssigned": "Total assigned",
    "dashboard.stats.totalPrefix": "Total:",
    "dashboard.stats.wip": "WIP",
    "dashboard.stats.yourCompletion": "YOUR COMPLETION",
    "dashboard.stats.yourFlow": "YOUR FLOW",
    "dashboard.stats.yourWorkload": "YOUR WORKLOAD",
    "dashboard.tabs.dashboard": "Dashboard",
    "dashboard.tabs.projects": "Projects",
    "dashboard.throughput.barTitle": "{weekStart}: {stories} stories, {points} pts",
    "dashboard.title": "Dashboard",
    "dashboard.todo.estimationPointsAria": "Estimation points",
    "errors.BAD_REQUEST": "Bad request",
    "errors.CONFLICT": "Conflict",
    "errors.FORBIDDEN": "Forbidden",
    "errors.INTERNAL": "Something went wrong.",
    "errors.METHOD_NOT_ALLOWED": "Method not allowed",
    "errors.NOT_FOUND": "Not found",
    "errors.PAYLOAD_TOO_LARGE": "Upload is too large.",
    "errors.RATE_LIMITED": "Too many attempts. Try again later.",
    "errors.SERVICE_UNAVAILABLE": "Service unavailable.",
    "errors.UNAUTHORIZED": "Unauthorized",
    "errors.VALIDATION_ERROR": "Please check the request and try again.",
    "errors.VALIDATION_ERROR.active_sprint_only_end_at": "Only the end date can be changed for an active sprint.",
    "errors.VALIDATION_ERROR.assignee_not_found": "Assignee not found.",
    "errors.VALIDATION_ERROR.assignee_not_project_member": "Assignee is not a project member.",
    "errors.VALIDATION_ERROR.assignment_not_allowed_anonymous": "Assignment is not available on anonymous boards.",
    "errors.VALIDATION_ERROR.body_too_large": "The todo body is too large.",
    "errors.VALIDATION_ERROR.cannot_delete_done_workflow_column": "The done workflow column cannot be deleted.",
    "errors.VALIDATION_ERROR.cannot_delete_last_owner": "The last owner cannot be deleted.",
    "errors.VALIDATION_ERROR.cannot_delete_self": "You cannot delete your own account.",
    "errors.VALIDATION_ERROR.cannot_demote_last_owner": "The last owner cannot be demoted.",
    "errors.VALIDATION_ERROR.cannot_link_todo_to_itself": "A todo cannot be linked to itself.",
    "errors.VALIDATION_ERROR.cannot_remove_last_maintainer": "The last maintainer cannot be removed.",
    "errors.VALIDATION_ERROR.closed_sprint_dates_locked": "Dates cannot be changed for a closed sprint.",
    "errors.VALIDATION_ERROR.color_required": "Please choose a color.",
    "errors.VALIDATION_ERROR.default_sprint_weeks_required": "Please choose a default sprint length.",
    "errors.VALIDATION_ERROR.duplicate_workflow_column_key": "Workflow column keys must be unique.",
    "errors.VALIDATION_ERROR.edge_id_required": "Wall edge ID is required.",
    "errors.VALIDATION_ERROR.endpoint_required": "Endpoint is required.",
    "errors.VALIDATION_ERROR.file_must_be_image": "Please choose an image file.",
    "errors.VALIDATION_ERROR.image_too_large": "The image is too large.",
    "errors.VALIDATION_ERROR.image_wallpaper_requires_upload": "Image wallpaper must be uploaded first.",
    "errors.VALIDATION_ERROR.import_full_scope_anonymous_forbidden": "Full-scope imports are not allowed in anonymous mode.",
    "errors.VALIDATION_ERROR.invalid_color": "Please enter a valid color.",
    "errors.VALIDATION_ERROR.invalid_column_key": "Please choose a valid workflow column.",
    "errors.VALIDATION_ERROR.invalid_default_sprint_weeks": "Default sprint length must be 1 or 2 weeks.",
    "errors.VALIDATION_ERROR.invalid_email": "Please enter a valid email address.",
    "errors.VALIDATION_ERROR.invalid_estimation_points": "Please choose valid estimation points.",
    "errors.VALIDATION_ERROR.invalid_image": "Please choose a valid image.",
    "errors.VALIDATION_ERROR.invalid_import_mode": "Please choose a valid import mode.",
    "errors.VALIDATION_ERROR.invalid_json": "Please provide valid JSON.",
    "errors.VALIDATION_ERROR.invalid_link": "Please choose a valid todo link.",
    "errors.VALIDATION_ERROR.invalid_link_type": "Please choose a valid link type.",
    "errors.VALIDATION_ERROR.invalid_name": "Please enter a valid name.",
    "errors.VALIDATION_ERROR.invalid_project_id": "Please choose a valid project.",
    "errors.VALIDATION_ERROR.invalid_project_name": "Please enter a valid project name.",
    "errors.VALIDATION_ERROR.invalid_request_body": "Please provide a valid request body.",
    "errors.VALIDATION_ERROR.invalid_role": "Please choose a valid role.",
    "errors.VALIDATION_ERROR.invalid_slug": "Please enter a valid slug.",
    "errors.VALIDATION_ERROR.invalid_sprint_id": "Please choose a valid sprint.",
    "errors.VALIDATION_ERROR.invalid_sprint_name": "Please enter a valid sprint name.",
    "errors.VALIDATION_ERROR.invalid_system_role": "Please choose a valid system role.",
    "errors.VALIDATION_ERROR.invalid_tag": "Please enter a valid tag.",
    "errors.VALIDATION_ERROR.invalid_tag_color": "Please choose a valid tag color.",
    "errors.VALIDATION_ERROR.invalid_tag_id": "Please choose a valid tag.",
    "errors.VALIDATION_ERROR.invalid_tag_name": "Please enter a valid tag name.",
    "errors.VALIDATION_ERROR.invalid_target_local_id": "Please choose a valid target todo.",
    "errors.VALIDATION_ERROR.invalid_title": "Please enter a valid title.",
    "errors.VALIDATION_ERROR.invalid_todo_id": "Please choose a valid todo.",
    "errors.VALIDATION_ERROR.invalid_todo_local_id": "Please choose a valid todo.",
    "errors.VALIDATION_ERROR.invalid_token_id": "Please choose a valid token.",
    "errors.VALIDATION_ERROR.invalid_trello_json": "Please provide valid Trello JSON.",
    "errors.VALIDATION_ERROR.invalid_user_id": "Please choose a valid user.",
    "errors.VALIDATION_ERROR.invalid_webhook_id": "Please choose a valid webhook.",
    "errors.VALIDATION_ERROR.invalid_workflow_column_color": "Please choose a valid workflow column color.",
    "errors.VALIDATION_ERROR.invalid_workflow_column_name": "Please enter a valid workflow column name.",
    "errors.VALIDATION_ERROR.invalid_workflow_key": "Please choose a valid workflow column.",
    "errors.VALIDATION_ERROR.missing_assignee_user_id": "Please choose an assignee.",
    "errors.VALIDATION_ERROR.missing_body": "Please provide a request body.",
    "errors.VALIDATION_ERROR.missing_data": "Please provide import data.",
    "errors.VALIDATION_ERROR.missing_to_column_key": "Please choose a destination column.",
    "errors.VALIDATION_ERROR.name_based_tag_route_not_allowed": "Please use the tag ID route for this project.",
    "errors.VALIDATION_ERROR.name_required": "Please enter a name.",
    "errors.VALIDATION_ERROR.note_id_required": "Wall note ID is required.",
    "errors.VALIDATION_ERROR.password_required": "Please enter your password.",
    "errors.VALIDATION_ERROR.processed_image_too_large": "The processed image is too large.",
    "errors.VALIDATION_ERROR.project_missing_name": "The imported project is missing a name.",
    "errors.VALIDATION_ERROR.project_missing_slug": "The imported project is missing a slug.",
    "errors.VALIDATION_ERROR.project_workflow_done_column_required": "Workflow must have exactly one done column.",
    "errors.VALIDATION_ERROR.project_workflow_min_columns": "Workflow must have at least 2 columns.",
    "errors.VALIDATION_ERROR.push_subscription_keys_required": "Push subscription keys are required.",
    "errors.VALIDATION_ERROR.replace_all_anonymous_forbidden": "Replace All is not allowed in anonymous mode.",
    "errors.VALIDATION_ERROR.replace_confirmation_required": "Type REPLACE to confirm replace mode.",
    "errors.VALIDATION_ERROR.self_edges_not_allowed": "A wall item cannot link to itself.",
    "errors.VALIDATION_ERROR.setup_token_and_code_required": "Setup token and code are required.",
    "errors.VALIDATION_ERROR.sprint_activate_requires_planned": "Only planned sprints can be activated.",
    "errors.VALIDATION_ERROR.sprint_end_before_start": "Sprint end date must be on or after the start date.",
    "errors.VALIDATION_ERROR.sprint_end_in_past": "Sprint end date must be in the future.",
    "errors.VALIDATION_ERROR.sprint_name_exists": "A sprint with this name already exists.",
    "errors.VALIDATION_ERROR.sprint_not_found": "Sprint not found.",
    "errors.VALIDATION_ERROR.sprint_not_in_project": "Sprint does not belong to this project.",
    "errors.VALIDATION_ERROR.target_board_not_anonymous": "The target board is not an anonymous board.",
    "errors.VALIDATION_ERROR.target_local_id_required": "Please choose a target todo.",
    "errors.VALIDATION_ERROR.temp_token_and_code_required": "Temporary token and code are required.",
    "errors.VALIDATION_ERROR.todo_missing_local_id": "The imported todo is missing a local ID.",
    "errors.VALIDATION_ERROR.todo_missing_title": "The imported todo is missing a title.",
    "errors.VALIDATION_ERROR.too_many_tags": "There are too many tags.",
    "errors.VALIDATION_ERROR.trello_import_validation_failed": "Trello import validation failed.",
    "errors.VALIDATION_ERROR.unsupported_export_version": "This export version is not supported.",
    "errors.VALIDATION_ERROR.use_null_to_clear_avatar": "Use null to clear the avatar.",
    "errors.VALIDATION_ERROR.wall_edge_endpoints_required": "Wall edge endpoints are required.",
    "errors.VALIDATION_ERROR.wall_edge_limit_reached": "Wall edge limit reached.",
    "errors.VALIDATION_ERROR.wall_note_limit_reached": "Wall note limit reached.",
    "errors.VALIDATION_ERROR.wall_note_too_long": "Wall note text is too long.",
    "errors.VALIDATION_ERROR.workflow_column_limit_reached": "Workflow column limit reached.",
    "errors.VALIDATION_ERROR.workflow_column_name_required": "Please enter a workflow column name.",
    "errors.generic": "Something went wrong.",
    "errors.httpStatus": "HTTP {status}",
    "nav.temporaryBoards.long": "Temporary Boards",
    "nav.temporaryBoards.short": "Temporary",
    "projects.actions.create": "Create",
    "projects.actions.createTemporaryBoard": "Create Temporary Board",
    "projects.actions.delete": "Delete",
    "projects.actions.rename": "Rename",
    "projects.actions.renameProject": "Rename project",
    "projects.actions.settings": "Settings",
    "projects.create.failed": "Failed to create project",
    "projects.delete.confirmMessage": "Delete this project and all its todos?",
    "projects.delete.failed": "Failed to delete project",
    "projects.empty.projects": "No projects yet.",
    "projects.empty.temporary": "No temporary boards yet.",
    "projects.fields.namePlaceholder": "New project name",
    "projects.rename.confirmAction": "Rename",
    "projects.rename.failed": "Failed to rename project",
    "projects.rename.label": "Project Name",
    "projects.rename.placeholder": "Project name",
    "projects.rename.success": "Project renamed",
    "projects.rename.title": "Rename Project",
    "projects.tabs.dashboard": "Dashboard",
    "projects.tabs.projects": "Projects",
    "projects.title": "Projects",
    "projects.validation.nameRequired": "Project name is required.",
    "projects.view.grid": "Grid view",
    "projects.view.list": "List view",
    "projects.workflow.addLaneAction": "Add",
    "projects.workflow.addLaneAriaLabel": "Add lane",
    "projects.workflow.addLanePlaceholder": "Add lane...",
    "projects.workflow.cancelAction": "Cancel",
    "projects.workflow.confirmAction": "Confirm",
    "projects.workflow.creating": "Creating...",
    "projects.workflow.doneLabel": "Done",
    "projects.workflow.helper": "Configure lanes before creating the project.",
    "projects.workflow.laneColor": "Lane color for {name}",
    "projects.workflow.reorderLane": "Reorder lane",
    "projects.workflow.setDoneLane": "Set {name} as done lane",
    "projects.workflow.title": "Customize Workflow",
    "projects.workflow.validation.duplicateKey": "Duplicate lane keys. Rename lanes to fix.",
    "projects.workflow.validation.emptyName": "Lane names cannot be empty.",
    "projects.workflow.validation.exactlyOneDone": "Exactly one lane must be marked as Done.",
    "projects.workflow.validation.invalidColor": "Lane colors must be valid hex colors.",
    "projects.workflow.validation.invalidKey": "Lane keys must be snake_case (letters, numbers, underscore).",
    "projects.workflow.validation.minLanes": "Workflow must have at least 2 lanes.",
    "realtime.assigned": "Assigned: {title}",
    "realtime.todoFallback": "Todo",
    "shell.bulkEdit.addTags": "Add tags",
    "shell.bulkEdit.assignSprint": "Assign sprint",
    "shell.bulkEdit.assignTo": "Assign to",
    "shell.bulkEdit.assignUser": "Assign user",
    "shell.bulkEdit.changeStatus": "Change status",
    "shell.bulkEdit.estimationPoints": "Estimation points",
    "shell.bulkEdit.noEstimate": "No estimate",
    "shell.bulkEdit.setEstimationPoints": "Set estimation points",
    "shell.bulkEdit.sprint": "Sprint",
    "shell.bulkEdit.status": "Status",
    "shell.bulkEdit.tagsPlaceholder": "Type tag and press Enter",
    "shell.contextMenu.newTodo": "New Todo",
    "settings.language.description": "Choose the language used for Scrumboy on this browser.",
    "settings.language.selectLabel": "Language",
    "settings.language.title": "Language",
    "todo.assignee.current": "Current: {name}",
    "todo.assignee.me": "Me",
    "todo.assignee.unassigned": "Unassigned",
    "todo.confirm.deleteAction": "Delete",
    "todo.confirm.deleteMessage": "Delete this todo?",
    "todo.confirm.deleteTitle": "Delete",
    "todo.confirm.discardAction": "Discard",
    "todo.confirm.discardMessage": "You have unsaved changes. Discard them?",
    "todo.confirm.discardTitle": "Unsaved changes",
    "todo.created": "Todo created",
    "todo.deleteFailed": "Failed to delete todo",
    "todo.dialog.title.edit": "Edit Todo",
    "todo.dialog.title.new": "New Todo",
    "todo.dialog.title.view": "View Todo",
    "todo.estimation.none": "No estimate",
    "todo.fields.assignedTo": "Assigned to",
    "todo.fields.estimationPoints": "Estimation Points",
    "todo.fields.linkedStories": "Linked Stories",
    "todo.fields.notes": "Notes",
    "todo.fields.sprint": "Sprint",
    "todo.fields.status": "Status",
    "todo.fields.tags": "Tags",
    "todo.fields.title": "Title",
    "todo.links.addPrompt": "Type #id or title, then tap Add",
    "todo.links.cannotShare": "Cannot share: no story in context",
    "todo.links.copySuccess": "Link copied",
    "todo.links.linkFailed": "Failed to link story",
    "todo.links.remove": "Remove link",
    "todo.links.removeFailed": "Failed to remove link",
    "todo.links.searchFailed": "Failed to search stories",
    "todo.links.searchPlaceholder": "Search by #id or title...",
    "todo.links.shareAriaLabel": "Share story link",
    "todo.links.shareFailed": "Share failed",
    "todo.links.shareSuccess": "Link shared",
    "todo.links.shareUnsupported": "Share not supported",
    "todo.links.storyFallbackTitle": "Story #{id}",
    "todo.loadLinkedFailed": "Failed to load linked stories",
    "todo.notes.markdown": "markdown",
    "todo.notes.modeLabel": "Notes editor mode",
    "todo.notes.preview": "preview",
    "todo.notes.previewUnavailable": "Markdown preview is unavailable",
    "todo.saveFailed": "Failed to save todo",
    "todo.sprint.state.ACTIVE": "Active",
    "todo.sprint.state.CLOSED": "Closed",
    "todo.sprint.state.PLANNED": "Planned",
    "todo.status.backlog": "Backlog",
    "todo.status.done": "Done",
    "todo.status.inProgress": "In Progress",
    "todo.status.notStarted": "Not Started",
    "todo.status.testing": "Testing",
    "todo.tags.placeholder": "Type tag and press Enter or Tab",
    "todo.updated": "Todo updated",
    "tooltips.boardSearch": "Search titles and notes. Combine with tag and sprint chips to narrow the board.",
    "tooltips.doneLane": "Exactly one lane counts as done. Stories there get a completion timestamp used for dashboard stats and burndown, even if the lane is named Shipped instead of Done.",
    "tooltips.estimationPoints": "Relative effort, not hours. Uses a modified Fibonacci scale (1\u201340). Compare to similar work on this board.",
    "tooltips.linkedStories": "Link related stories (dependencies, parent/child, duplicates). Search by local ID (#12) or title. Links are informational \u2014 they do not move cards automatically.",
    "tooltips.memberRole": "Viewer: read-only. Contributor: edit notes when assigned. Maintainer: create, move, assign, sprints, and settings.",
    "tooltips.sprintDefaultWeeks": "When you create a sprint, the end date defaults to this many weeks after the start date.",
    "tooltips.sprintEnd": "Planned end of this sprint. Burndown and dashboard completion stats use the sprint date range.",
    "tooltips.sprintFilterActive": "Currently active iteration \u2014 only one sprint can be active at a time.",
    "tooltips.sprintFilterScheduled": "Stories assigned to any sprint.",
    "tooltips.sprintFilterUnscheduled": "Stories not in a sprint yet (often your backlog).",
    "tooltips.sprintName": "A label for this iteration, e.g. Sprint 12 or 2026 Q1 Sprint 1.",
    "tooltips.sprintStart": "Planned start of this sprint. Burndown and dashboard completion stats use the sprint date range.",
    "tooltips.sprintTodo": "Which time-boxed iteration this story belongs to. Leave empty if not scheduled yet.",
    "tooltips.status": "Which workflow lane this story is in. Done is whichever lane is marked as done in Settings \u2192 Workflow; that lane drives dashboard completion stats.",
    "tooltips.tags": "Free-form labels for filtering and grouping. On shared boards, tag colors are the same for everyone; your personal tag colors apply across your projects.",
    "tooltips.voiceCommand": "Story and todo mean the same thing. Use a local ID (12, #12) or a title phrase. One clear command per line \u2014 no pronouns like it or that.",
    "tooltips.workflowAddLane": "Adds a new column before the done lane. Lane names can be renamed later; internal keys stay fixed.",
};
const HYDRATION_BINDINGS = [
    ["data-i18n-text", "textContent"],
    ["data-i18n-aria-label", "aria-label"],
    ["data-i18n-placeholder", "placeholder"],
    ["data-i18n-title", "title"],
];
let activeLocale = "en";
let activeCatalog = BOOTSTRAP_EN_CATALOG;
let englishCatalog = BOOTSTRAP_EN_CATALOG;
let loader = defaultLoadLocale;
const catalogCache = new Map();
const warnedMissingKeys = new Set();
function getNodeEnv() {
    return String((globalThis.process?.env?.NODE_ENV) || "");
}
function getDefaultStorage() {
    try {
        return globalThis.localStorage || null;
    }
    catch {
        return null;
    }
}
function getDefaultLanguages() {
    const nav = globalThis.navigator;
    if (Array.isArray(nav?.languages) && nav.languages.length > 0) {
        return nav.languages;
    }
    return nav?.language ? [nav.language] : [];
}
function getDefaultDocumentElement() {
    return globalThis.document?.documentElement || null;
}
function getStoredLocale(storage) {
    try {
        return normalizeLocale(storage?.getItem(LOCALE_STORAGE_KEY));
    }
    catch {
        return null;
    }
}
export function normalizeLocale(value) {
    if (!value)
        return null;
    const normalized = value.trim().toLowerCase().replace("_", "-");
    if (normalized === "pseudo")
        return "pseudo";
    if (normalized === "de" || normalized.startsWith("de-"))
        return "de";
    if (normalized === "it" || normalized.startsWith("it-"))
        return "it";
    if (normalized === "ms" || normalized === "ms-my" || normalized.startsWith("ms-"))
        return "ms";
    if (normalized === "pl" || normalized.startsWith("pl-"))
        return "pl";
    if (normalized === "uk" || normalized === "uk-ua" || normalized.startsWith("uk-"))
        return "uk";
    if (normalized === "fr" || normalized.startsWith("fr-"))
        return "fr";
    if (normalized === "bn" || normalized === "bn-bd" || normalized === "bn-in" || normalized.startsWith("bn-"))
        return "bn";
    if (normalized === "pt" || normalized.startsWith("pt-"))
        return "pt";
    if (normalized === "es" || normalized.startsWith("es-"))
        return "es";
    if (normalized === "ar" || normalized.startsWith("ar-"))
        return "ar";
    if (normalized === "ru" || normalized.startsWith("ru-"))
        return "ru";
    if (normalized === "ja" || normalized.startsWith("ja-"))
        return "ja";
    if (normalized === "sw" || normalized === "sw-tz" || normalized.startsWith("sw-"))
        return "sw";
    if (normalized === "tr" || normalized.startsWith("tr-"))
        return "tr";
    if (normalized === "ko" || normalized.startsWith("ko-"))
        return "ko";
    if (normalized === "zh"
        || normalized === "zh-cn"
        || normalized === "zh-hans"
        || normalized === "zh-sg"
        || normalized.startsWith("zh-cn-")
        || normalized.startsWith("zh-hans-")) {
        return "zh";
    }
    if (normalized === "id" || normalized.startsWith("id-"))
        return "id";
    if (normalized === "vi" || normalized.startsWith("vi-"))
        return "vi";
    if (normalized === "th" || normalized.startsWith("th-"))
        return "th";
    if (normalized === "ur" || normalized.startsWith("ur-"))
        return "ur";
    if (normalized === "fa" || normalized === "fa-ir" || normalized === "fa-af" || normalized.startsWith("fa-"))
        return "fa";
    if (normalized === "hi" || normalized.startsWith("hi-"))
        return "hi";
    if (normalized === "en" || normalized.startsWith("en-"))
        return "en";
    return null;
}
export function isRtlLocale(locale) {
    return locale === "ar" || locale === "ur" || locale === "fa";
}
export function documentDirection(locale) {
    return isRtlLocale(locale) ? "rtl" : "ltr";
}
export function isPublicLocale(locale) {
    return PUBLIC_LOCALES.includes(locale);
}
export function publicLocaleOptions() {
    return PUBLIC_LOCALES.map((id) => ({
        id,
        label: LOCALE_LABELS[id],
        flagSrc: PUBLIC_LOCALE_FLAG_PATHS[id],
    }));
}
function normalizeBrowserLanguageTag(value) {
    return value.trim().toLowerCase().replace(/_/g, "-");
}
export function browserLanguageMatchesPublicLocale(landingLocale, languages) {
    const locale = landingLocale.toLowerCase();
    for (const language of languages) {
        const tag = normalizeBrowserLanguageTag(String(language));
        if (!tag)
            continue;
        if (tag === locale || tag.startsWith(`${locale}-`)) {
            return true;
        }
    }
    return false;
}
export function detectLocale(options = {}) {
    const storage = options.storage === undefined ? getDefaultStorage() : options.storage;
    const stored = getStoredLocale(storage);
    if (stored)
        return stored;
    const languages = options.languages ?? getDefaultLanguages();
    for (const language of languages) {
        const locale = normalizeLocale(language);
        if (locale)
            return locale;
    }
    return "en";
}
function getAppVersion() {
    const meta = globalThis.document?.querySelector?.('meta[name="app-version"]');
    return meta?.getAttribute("content") || "";
}
async function defaultLoadLocale(locale) {
    if (typeof fetch !== "function") {
        throw new Error("Cannot load i18n catalog: fetch is unavailable");
    }
    const version = getAppVersion();
    const suffix = version ? `?v=${encodeURIComponent(version)}` : "";
    const res = await fetch(`/dist/i18n/locales/${locale}.json${suffix}`, {
        headers: { Accept: "application/json" },
    });
    if (!res.ok) {
        throw new Error(`Failed to load i18n catalog ${locale}: HTTP ${res.status}`);
    }
    return normalizeCatalog(await res.json(), locale);
}
function normalizeCatalog(raw, locale) {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
        throw new Error(`Invalid i18n catalog ${locale}: expected object`);
    }
    const catalog = {};
    for (const [key, value] of Object.entries(raw)) {
        if (typeof value !== "string") {
            throw new Error(`Invalid i18n catalog ${locale}: ${key} must be a string`);
        }
        catalog[key] = value;
    }
    return catalog;
}
async function ensureLocaleLoaded(locale) {
    const cached = catalogCache.get(locale);
    if (cached)
        return cached;
    const catalog = await loader(locale);
    catalogCache.set(locale, catalog);
    if (locale === "en")
        englishCatalog = catalog;
    return catalog;
}
function updateDocumentLang(locale, element = getDefaultDocumentElement()) {
    if (!element)
        return;
    element.lang = locale === "pseudo" ? "en" : intlLocale(locale);
    element.setAttribute("data-locale", locale);
    const dir = documentDirection(locale);
    if (dir === "rtl") {
        element.setAttribute("dir", "rtl");
    }
    else {
        element.removeAttribute("dir");
    }
}
function persistLocale(locale, storage = getDefaultStorage()) {
    try {
        storage?.setItem(LOCALE_STORAGE_KEY, locale);
    }
    catch {
        // Storage is best effort; the active in-memory locale still changes.
    }
    persistLocaleCookie(locale);
}
function persistLocaleCookie(locale) {
    if (isPublicLocale(locale)) {
        writeLocaleCookie(locale);
        return;
    }
    clearLocaleCookie();
}
function writeLocaleCookie(locale) {
    try {
        const doc = globalThis.document;
        if (!doc)
            return;
        const secure = globalThis.location?.protocol === "https:" ? "; Secure" : "";
        doc.cookie = `${LOCALE_STORAGE_KEY}=${encodeURIComponent(locale)}; Path=/; Max-Age=${LOCALE_COOKIE_MAX_AGE_SECONDS}; SameSite=Lax${secure}`;
    }
    catch {
        // Cookie persistence is best effort and only supports server-side landing negotiation.
    }
}
function clearLocaleCookie() {
    try {
        const doc = globalThis.document;
        if (!doc)
            return;
        const secure = globalThis.location?.protocol === "https:" ? "; Secure" : "";
        doc.cookie = `${LOCALE_STORAGE_KEY}=; Path=/; Max-Age=0; Expires=Thu, 01 Jan 1970 00:00:00 GMT; SameSite=Lax${secure}`;
    }
    catch {
        // Cookie persistence is best effort and only supports server-side landing negotiation.
    }
}
function dispatchLocaleChanged(locale) {
    const eventTarget = globalThis.document;
    if (!eventTarget || typeof eventTarget.dispatchEvent !== "function")
        return;
    eventTarget.dispatchEvent(new CustomEvent(I18N_LOCALE_CHANGED, { detail: { locale } }));
}
export async function initI18n(options = {}) {
    if (options.loadLocale) {
        loader = options.loadLocale;
        catalogCache.clear();
        activeLocale = "en";
        englishCatalog = BOOTSTRAP_EN_CATALOG;
        activeCatalog = BOOTSTRAP_EN_CATALOG;
    }
    const storage = options.storage === undefined ? getDefaultStorage() : options.storage;
    const storedLocale = getStoredLocale(storage);
    const desiredLocale = normalizeLocale(options.locale) ||
        storedLocale ||
        detectLocale({ storage: null, languages: options.languages });
    const en = await ensureLocaleLoaded("en");
    let nextLocale = desiredLocale;
    let nextCatalog = en;
    if (desiredLocale !== "en") {
        try {
            nextCatalog = await ensureLocaleLoaded(desiredLocale);
        }
        catch (err) {
            console.warn(`Falling back to English because locale "${desiredLocale}" failed to load.`, err);
            nextLocale = "en";
            nextCatalog = en;
        }
    }
    activeLocale = nextLocale;
    activeCatalog = nextCatalog;
    updateDocumentLang(activeLocale, options.documentElement ?? getDefaultDocumentElement());
    if (options.persist === true && storage) {
        persistLocale(activeLocale, storage);
    }
    else if (storedLocale && activeLocale === storedLocale) {
        persistLocaleCookie(storedLocale);
    }
    return activeLocale;
}
export async function setLocale(locale) {
    const previousLocale = activeLocale;
    const previousCatalog = activeCatalog;
    const nextLocale = normalizeLocale(locale) || "en";
    const en = await ensureLocaleLoaded("en");
    let nextCatalog = en;
    let resolvedLocale = nextLocale;
    if (nextLocale !== "en") {
        try {
            nextCatalog = await ensureLocaleLoaded(nextLocale);
        }
        catch (err) {
            console.warn(`Falling back to English because locale "${nextLocale}" failed to load.`, err);
            resolvedLocale = "en";
        }
    }
    activeLocale = resolvedLocale;
    activeCatalog = nextCatalog;
    persistLocale(activeLocale);
    updateDocumentLang(activeLocale);
    if (previousLocale !== activeLocale || previousCatalog !== activeCatalog) {
        dispatchLocaleChanged(activeLocale);
    }
    return activeLocale;
}
export function getLocale() {
    return activeLocale;
}
function hasOwnMessage(catalog, key) {
    return Object.prototype.hasOwnProperty.call(catalog, key);
}
function strictMissingKeyMode() {
    const env = getNodeEnv();
    if (env === "test")
        return "throw";
    if (env === "development")
        return "warn";
    if (env === "production")
        return "off";
    const hostname = globalThis.location?.hostname || "";
    if (hostname === "localhost" || hostname === "127.0.0.1" || hostname === "::1") {
        return "warn";
    }
    return "off";
}
function reportMissingKey(locale, key) {
    const message = `Missing i18n key "${key}" for locale "${locale}"`;
    const mode = strictMissingKeyMode();
    if (mode === "throw") {
        throw new Error(message);
    }
    if (mode === "warn" && !warnedMissingKeys.has(message)) {
        warnedMissingKeys.add(message);
        console.warn(message);
    }
}
function resolveMessage(key) {
    if (hasOwnMessage(activeCatalog, key)) {
        return activeCatalog[key];
    }
    const fallback = englishCatalog[key];
    reportMissingKey(activeLocale, key);
    return fallback || key;
}
function interpolate(message, values) {
    return message.replace(/\{([a-zA-Z0-9_.-]+)\}/g, (match, name) => {
        const value = values[name];
        return value == null ? match : String(value);
    });
}
export function t(key, values = {}) {
    return interpolate(resolveMessage(key), values);
}
function elementsForAttribute(root, attributeName) {
    const elements = [];
    if (typeof Element !== "undefined" && root instanceof Element && root.hasAttribute(attributeName)) {
        elements.push(root);
    }
    root.querySelectorAll?.(`[${attributeName}]`).forEach((element) => elements.push(element));
    return elements;
}
export function hydrateI18n(root = globalThis.document) {
    if (!root)
        return;
    for (const [sourceAttribute, targetAttribute] of HYDRATION_BINDINGS) {
        for (const element of elementsForAttribute(root, sourceAttribute)) {
            const key = element.getAttribute(sourceAttribute);
            if (!key)
                continue;
            const message = t(key);
            if (targetAttribute === "textContent") {
                element.textContent = message;
            }
            else {
                element.setAttribute(targetAttribute, message);
            }
        }
    }
}
export function hasI18nKey(key) {
    return hasOwnMessage(activeCatalog, key) || hasOwnMessage(englishCatalog, key);
}
function intlLocale(locale = activeLocale) {
    if (locale === "pseudo")
        return "en";
    if (locale === "pt")
        return "pt-BR";
    if (locale === "es")
        return "es-MX";
    if (locale === "it")
        return "it-IT";
    if (locale === "ms")
        return "ms-MY";
    if (locale === "pl")
        return "pl-PL";
    if (locale === "uk")
        return "uk-UA";
    if (locale === "zh")
        return "zh-CN";
    if (locale === "id")
        return "id-ID";
    if (locale === "vi")
        return "vi-VN";
    if (locale === "th")
        return "th-TH";
    if (locale === "ur")
        return "ur-PK";
    if (locale === "fa")
        return "fa-IR";
    if (locale === "hi")
        return "hi-IN";
    if (locale === "bn")
        return "bn-BD";
    if (locale === "sw")
        return "sw-TZ";
    return locale;
}
export function formatDate(value, options) {
    const date = value instanceof Date ? value : new Date(value);
    return new Intl.DateTimeFormat(intlLocale(), options).format(date);
}
function ordinalSuffix(n) {
    const s = n % 100;
    if (s >= 11 && s <= 13)
        return "th";
    switch (n % 10) {
        case 1: return "st";
        case 2: return "nd";
        case 3: return "rd";
        default: return "th";
    }
}
export function formatLongDateWithWeekday(value) {
    const date = value instanceof Date ? value : new Date(value);
    if (intlLocale() === "en") {
        return `${formatDate(date, { weekday: "long" })}, ${formatDate(date, { month: "long" })} ${date.getDate()}${ordinalSuffix(date.getDate())} ${date.getFullYear()}`;
    }
    return formatDate(date, {
        weekday: "long",
        month: "long",
        day: "numeric",
        year: "numeric",
    });
}
export function formatNumber(value, options) {
    return new Intl.NumberFormat(intlLocale(), options).format(value);
}
function extractErrorBody(err) {
    const maybe = err;
    const data = maybe?.data ?? err;
    return data && typeof data === "object" ? data : null;
}
function nonEmptyString(value) {
    return typeof value === "string" && value.trim() ? value : null;
}
export function apiErrorMessage(err, options = {}) {
    const body = extractErrorBody(err);
    const error = body?.error;
    const details = error?.details || undefined;
    const reason = typeof details?.reason === "string" ? details.reason : "";
    const code = typeof error?.code === "string" ? error.code : "";
    if (code) {
        const reasonKey = reason ? `errors.${code}.${reason}` : "";
        if (reasonKey && hasI18nKey(reasonKey)) {
            return t(reasonKey, details);
        }
        const codeKey = `errors.${code}`;
        if (hasI18nKey(codeKey)) {
            return t(codeKey, (details || {}));
        }
    }
    if (options.fallbackKey && hasI18nKey(options.fallbackKey)) {
        return t(options.fallbackKey, (details || {}));
    }
    const rawApiMessage = nonEmptyString(error?.message);
    if (rawApiMessage) {
        return rawApiMessage;
    }
    const rawMessage = nonEmptyString(err?.message);
    if (rawMessage) {
        return rawMessage;
    }
    const status = err?.status;
    if (typeof status === "number" && Number.isFinite(status)) {
        return t("errors.httpStatus", { status });
    }
    return t("errors.generic");
}
export function apiErrorMessageOrRaw(err, options = {}) {
    const body = extractErrorBody(err);
    const error = body?.error;
    const details = error?.details || undefined;
    const reason = typeof details?.reason === "string" ? details.reason : "";
    const code = typeof error?.code === "string" ? error.code : "";
    const reasonKey = code && reason ? `errors.${code}.${reason}` : "";
    if (reasonKey && hasI18nKey(reasonKey)) {
        return apiErrorMessage(err, options);
    }
    const rawApiMessage = nonEmptyString(error?.message);
    if (rawApiMessage) {
        return rawApiMessage;
    }
    const rawMessage = nonEmptyString(err?.message);
    if (rawMessage) {
        return rawMessage;
    }
    return t(options.fallbackKey || "errors.generic");
}
export function resetI18nForTests() {
    activeLocale = "en";
    activeCatalog = BOOTSTRAP_EN_CATALOG;
    englishCatalog = BOOTSTRAP_EN_CATALOG;
    loader = defaultLoadLocale;
    catalogCache.clear();
    warnedMissingKeys.clear();
    clearLocaleCookie();
}
