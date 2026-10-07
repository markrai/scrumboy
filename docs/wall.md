# Wall (Scrumbaby)

Spatial canvas for sticky notes and canonical project Stories/Todos on durable projects. Open it from the board topbar (desktop only) or press **W** on the board. A pinned story is not a copy: its title, workflow lane, tags, assignee, sprint, estimation, priority, archive state, and other content remain owned by the normal Todo.

## Controls

- **Close** - Click the **x** in the top-right corner.
- **New note** - **Right-click** empty canvas.
- **New story at a specific position** - Hold **Ctrl** and **right-click** empty canvas. Complete the normal New Todo dialog; after the server creates the Todo, its card is pinned at the captured point. Canceling or a failed create leaves no Wall placement.
- **Send an existing story to the Wall** - **Right-click** a board card and choose **Send to Wall**. The Wall opens and highlights the story. Repeating the command focuses the existing placement instead of duplicating it.
- **Open a pinned story** - Click it, or right-click it and choose **Open Story**, to use the normal Todo dialog.
- **Move a pinned story** - Drag its card. This changes only Wall coordinates, never its board lane or rank.
- **Remove a pinned story** - Right-click it and choose **Remove from Wall**. The canonical Todo remains on the board.
- **Move a note** - Drag the note (not the resize corner).
- **Resize** - Drag the **bottom-right** handle.
- **Change color** - **Single-click** a note (waits briefly so a double-click can still open edit).
- **Edit text** - **Double-click** a note. **Enter** commits (Shift+Enter = new line). **Escape** commits. **Blur** commits.
- **Note actions menu** - **Right-click** a note to open a small menu with:
  - **Create Todo from Note** - opens the **New Todo** dialog with the note's text prefilled as the Title. Save or cancel as usual; the wall stays open either way.
  - **Delete** - prompts the same confirmation as before, then deletes the note.
- **Delete a note (drag-to-trash)** - Drag it onto the **trash** image (bottom-right), then confirm.
- **Draw a line** - Hold **Shift**, drag between notes and pinned stories.
- **Delete a line** - **Right-click** the line, then confirm.
- **Select several notes** - **Drag** on empty canvas to draw a selection box.
- **Add or remove from selection** - **Ctrl**+click (Windows/Linux) or **⌘**+click (Mac) a note.
- **Exit multi-select on canvas** - **Click** empty space (no drag).
- **Move a group** - With multiple notes selected, drag one of them; all selected notes move together. Selection clears when you release the drag.
- **Delete several at once** - Drag the group over the trash, or press **Delete** when notes are selected; one confirmation lists how many notes will be deleted (or asks to delete a single selected note).
- **Canvas mode toggle** - The button left of **Fit view** switches between **Select** mode (dashed-square icon) and **Pan** mode (hand icon), or press **S**. Your choice is remembered globally in the browser and applies to every project's wall (defaults to Select until you change it).
- **Select mode** - Empty-canvas drag draws the marquee box to select notes.
- **Pan mode** - Empty-canvas mouse drag or touch swipe pans the wall. Two-finger touch pinch zooms the wall.
- **Pan the canvas** - Scroll wheel, **middle-mouse drag**, hold **Space** and drag on empty canvas, or use the **arrow keys** (hold **Shift** for larger steps).
- **Zoom** - **Shift**+scroll. **Ctrl**+scroll (Windows/Linux) or **⌘**+scroll (Mac) also zoom, and pinch-to-zoom on trackpads uses that modifier.
- **Fit view** - Click the **⊡** button (top-right, beside close) or press **F** while the wall is open. Recenters on all notes and pinned stories (or origin when empty). Your pan/zoom per board is remembered in the browser.

Viewers can open the Wall and pinned stories read-only. Contributors and maintainers can pin, move, and remove story placements under the same Wall mutation permission checks used for sticky-note writes. Archived stories remain visible with a muted treatment; hard-deleting a Todo removes its placement automatically.

## Disabling the wall

The wall is on by default. To turn it off for the whole server, set **`SCRUMBOY_WALL_ENABLED`** before starting Scrumboy. Any of these values disables it (trimmed, case-insensitive): **`0`**, **`false`**, **`off`**, **`no`**. If the variable is unset or empty, the wall stays enabled. Restart the process after changing env vars.
