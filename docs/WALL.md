# Wall (Scrumbaby)

Sticky-note board for durable projects. Open it from the board topbar (desktop only).

## Controls

- **Close** - Click the **x** in the top-right corner.
- **New note** - **Right-click** empty canvas.
- **Move a note** - Drag the note (not the resize corner).
- **Resize** - Drag the **bottom-right** handle.
- **Change color** - **Single-click** a note (waits briefly so a double-click can still open edit).
- **Edit text** - **Double-click** a note. **Enter** commits (Shift+Enter = new line). **Escape** cancels. **Blur** commits.
- **Note actions menu** - **Right-click** a note to open a small menu with:
  - **Create Todo from Note** - opens the **New Todo** dialog with the note's text prefilled as the Title. Save or cancel as usual; the wall stays open either way.
  - **Delete** - prompts the same confirmation as before, then deletes the note.
- **Delete a note (drag-to-trash)** - Drag it onto the **trash** image (bottom-right), then confirm.
- **Line between two notes** - Hold **Shift**, drag from one note to another.
- **Delete a line** - **Right-click** the line, then confirm.
- **Select several notes** - **Drag** on empty canvas to draw a selection box.
- **Add or remove from selection** - **Ctrl**+click (Windows/Linux) or **⌘**+click (Mac) a note.
- **Exit multi-select on canvas** - **Click** empty space (no drag).
- **Move a group** - With multiple notes selected, drag one of them; all selected notes move together. Selection clears when you release the drag.
- **Delete several at once** - Drag the group over the trash; one confirmation lists how many notes will be deleted.

## Disabling the wall

The wall is on by default. To turn it off for the whole server, set **`SCRUMBOY_WALL_ENABLED`** before starting Scrumboy. Any of these values disables it (trimmed, case-insensitive): **`0`**, **`false`**, **`off`**, **`no`**. If the variable is unset or empty, the wall stays enabled. Restart the process after changing env vars.
