// Browser entry for vendor/codemirror.min.js: the CodeMirror 6 subset the notes editor uses
// (modules/dialogs/todo-notes-editor.ts), exposed as window.ScrumboyCodeMirror.
//
// notesKeymap is CodeMirror's standardKeymap with Enter inserting a plain newline (like the
// textarea it replaces) instead of language-aware indentation, plus undo/redo.
import { Compartment, EditorSelection, EditorState, StateEffect, StateField, Transaction } from "@codemirror/state";
import { Decoration, EditorView, WidgetType, keymap } from "@codemirror/view";
import {
  cursorCharLeft, cursorCharRight, cursorDocEnd, cursorDocStart, cursorGroupLeft, cursorGroupRight,
  cursorLineBoundaryBackward, cursorLineBoundaryForward, cursorLineBoundaryLeft, cursorLineBoundaryRight,
  cursorLineDown, cursorLineUp, cursorPageDown, cursorPageUp, deleteCharBackward, deleteCharForward,
  deleteGroupBackward, deleteGroupForward, deleteLineBoundaryBackward, deleteLineBoundaryForward,
  history, historyKeymap, insertNewline, isolateHistory, redo, selectAll, selectCharLeft, selectCharRight, selectDocEnd,
  selectDocStart, selectGroupLeft, selectGroupRight, selectLineBoundaryBackward, selectLineBoundaryForward,
  selectLineBoundaryLeft, selectLineBoundaryRight, selectLineDown, selectLineUp, selectPageDown,
  selectPageUp, undo,
} from "@codemirror/commands";

const notesKeymap = [
  { key: "ArrowLeft", run: cursorCharLeft, shift: selectCharLeft, preventDefault: true },
  { key: "Mod-ArrowLeft", mac: "Alt-ArrowLeft", run: cursorGroupLeft, shift: selectGroupLeft, preventDefault: true },
  { mac: "Cmd-ArrowLeft", run: cursorLineBoundaryLeft, shift: selectLineBoundaryLeft, preventDefault: true },
  { key: "ArrowRight", run: cursorCharRight, shift: selectCharRight, preventDefault: true },
  { key: "Mod-ArrowRight", mac: "Alt-ArrowRight", run: cursorGroupRight, shift: selectGroupRight, preventDefault: true },
  { mac: "Cmd-ArrowRight", run: cursorLineBoundaryRight, shift: selectLineBoundaryRight, preventDefault: true },
  { key: "ArrowUp", run: cursorLineUp, shift: selectLineUp, preventDefault: true },
  { mac: "Cmd-ArrowUp", run: cursorDocStart, shift: selectDocStart },
  { mac: "Ctrl-ArrowUp", run: cursorPageUp, shift: selectPageUp },
  { key: "ArrowDown", run: cursorLineDown, shift: selectLineDown, preventDefault: true },
  { mac: "Cmd-ArrowDown", run: cursorDocEnd, shift: selectDocEnd },
  { mac: "Ctrl-ArrowDown", run: cursorPageDown, shift: selectPageDown },
  { key: "PageUp", run: cursorPageUp, shift: selectPageUp },
  { key: "PageDown", run: cursorPageDown, shift: selectPageDown },
  { key: "Home", run: cursorLineBoundaryBackward, shift: selectLineBoundaryBackward, preventDefault: true },
  { key: "Mod-Home", run: cursorDocStart, shift: selectDocStart },
  { key: "End", run: cursorLineBoundaryForward, shift: selectLineBoundaryForward, preventDefault: true },
  { key: "Mod-End", run: cursorDocEnd, shift: selectDocEnd },
  { key: "Enter", run: insertNewline, shift: insertNewline },
  { key: "Mod-a", run: selectAll },
  { key: "Backspace", run: deleteCharBackward, shift: deleteCharBackward, preventDefault: true },
  { key: "Delete", run: deleteCharForward, preventDefault: true },
  { key: "Mod-Backspace", mac: "Alt-Backspace", run: deleteGroupBackward, preventDefault: true },
  { key: "Mod-Delete", mac: "Alt-Delete", run: deleteGroupForward, preventDefault: true },
  { mac: "Mod-Backspace", run: deleteLineBoundaryBackward, preventDefault: true },
  { mac: "Mod-Delete", run: deleteLineBoundaryForward, preventDefault: true },
  ...historyKeymap,
  // historyKeymap binds Ctrl-Shift-z only on Linux; textareas accept it everywhere.
  { key: "Mod-Shift-z", run: redo, preventDefault: true },
];

globalThis.ScrumboyCodeMirror = Object.freeze({
  Compartment,
  Decoration,
  EditorSelection,
  EditorState,
  EditorView,
  StateEffect,
  StateField,
  Transaction,
  WidgetType,
  history,
  isolateHistory,
  keymap,
  notesKeymap,
  redo,
  undo,
});
