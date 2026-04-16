import { getUser } from '../state/selectors.js';
import { isAnonymousBoard, isTemporaryBoard } from '../utils.js';

export type TodoDialogPermissions = {
  canChangeSprint: boolean;
  canChangeEstimation: boolean;
  canEditTags: boolean;
  canEditNotes: boolean;
  canEditAssignment: boolean;
  canDeleteTodo: boolean;
  canEditTitle: boolean;
  canEditStatus: boolean;
  canSubmitTodo: boolean;
  canEditLinks: boolean;
};

const defaultPermissions: TodoDialogPermissions = {
  canChangeSprint: false,
  canChangeEstimation: false,
  canEditTags: false,
  canEditNotes: false,
  canEditAssignment: false,
  canDeleteTodo: false,
  canEditTitle: false,
  canEditStatus: false,
  canSubmitTodo: false,
  canEditLinks: false,
};

let permissions: TodoDialogPermissions = { ...defaultPermissions };

export function computeTodoDialogPermissions(opts: {
  board: any;
  mode: string;
  todo?: any;
  role?: string | null;
}): TodoDialogPermissions {
  const { board, mode, todo, role } = opts;
  const anonymousBoard = isAnonymousBoard(board);
  const temporaryBoard = isTemporaryBoard(board);
  const baseMaintainer = (role ?? "") === "maintainer" || anonymousBoard;
  const tempLinkForm = temporaryBoard && (mode === "create" || mode === "edit");
  const roleNorm = (role ?? "").toLowerCase();
  const isContributor = roleNorm === "contributor" || roleNorm === "editor";
  const currentUser = getUser();
  const isAssignedToMe = !!(
    currentUser &&
    mode === "edit" &&
    Number(todo?.assigneeUserId) === Number(currentUser.id)
  );
  const canEditTitle = baseMaintainer || tempLinkForm;
  const canEditStatus = baseMaintainer || tempLinkForm;
  const canSubmitTodo =
    mode === "create"
      ? baseMaintainer || tempLinkForm
      : baseMaintainer ||
        tempLinkForm ||
        (!anonymousBoard && isContributor && isAssignedToMe);
  const canEditLinks = baseMaintainer || (!anonymousBoard && isContributor);

  return {
    canChangeSprint: baseMaintainer && !anonymousBoard,
    canChangeEstimation: baseMaintainer || tempLinkForm,
    canEditTags: baseMaintainer || tempLinkForm,
    canEditNotes:
      baseMaintainer ||
      tempLinkForm ||
      (!anonymousBoard && isContributor && isAssignedToMe),
    canEditAssignment: baseMaintainer && !anonymousBoard,
    canDeleteTodo: baseMaintainer,
    canEditTitle,
    canEditStatus,
    canSubmitTodo,
    canEditLinks,
  };
}

export function setTodoFormPermissions(next: TodoDialogPermissions): void {
  permissions = { ...next };
}

export function getTodoFormPermissions(): Readonly<TodoDialogPermissions> {
  return { ...permissions };
}
