# Inline images in story notes

Story notes can contain images: paste a screenshot into the notes, or drag an image file onto them. The image appears right where you pasted or dropped it, and you can select, resize and remove it in place. Scrumboy stores the image and keeps the note as Markdown such as:

```markdown
![screenshot](/api/assets/550e8400-e29b-41d4-a716-446655440000)
```

Images show in the notes editor, in the note **Preview**, in read-only story dialogs, and in the story dialog of a [public board](public-boards.md).

## Editing images

The notes editor shows every Scrumboy image in place of its Markdown; everything else in the note is edited as plain Markdown text, exactly as before.

- **Size.** A new image is shown at most 320 px wide and 240 px tall, keeping its proportions. Smaller images are never enlarged, and no image is wider than the notes field.
- **Select.** Click or tap an image to select it (an outline around the image and a square resize handle on its bottom-right corner appear; bottom-left in right-to-left languages). From the keyboard, move the caret next to an image and press Shift+Arrow to select it. Screen readers announce the selected image and the keys below.
- **Resize.** Drag the resize handle (mouse or touch). The image keeps its proportions, so width and height change together, and stays between 48 px and the image's own width and the width of the notes field. With an image selected, **+** and **-** resize it, and **0** returns it to the default size.
- **Remove.** Press Delete or Backspace with the image selected, or Backspace right after it. Only the reference is removed from the note; the stored file is cleaned up later if no note uses it (see [Storage](#storage-budgets-and-cleanup)). **Undo** (Ctrl+Z / Cmd+Z) brings it back, and **Redo** (Ctrl+Y or Ctrl+Shift+Z) removes it again.
- **Escape** deselects the image without closing the story.
- **Pasting over text.** An image pasted or dropped over selected text replaces that text once the image has uploaded, as a single undo step. If the upload fails or is cancelled, the text stays; if you edit the selected text while the image uploads, your edit stays and the image is added next to it.

Images inside code (`` `...` `` or fenced blocks) are text, and stay text in the editor.

### Image width in Markdown

A resized image stores its display width, in CSS pixels, at the end of its alt text after `|`:

```markdown
![screenshot|480](/api/assets/550e8400-e29b-41d4-a716-446655440000)
```

- The width is a whole number from 48 to 1600, without leading zeros. Any other text after `|` (for example `![Q3|2024](...)`) is ordinary alt text, and the image is shown at the default size.
- The width includes the image's 1 px border, is always capped by the available width (so phones show the image smaller), and is the only metadata stored: notes never contain HTML, heights, or image data.
- The destination is unchanged, so the server recognizes the image exactly as before, and backups, `.sbz` exports and imports, public-board visibility and cleanup all treat sized and unsized images alike.
- Older Scrumboy versions and other Markdown tools show the image at its natural size; the alt text then includes the `|480` suffix.
- Resizing changes only those characters of the note. The rest of the note, including line endings (CRLF, LF, lone CR, or a mix), is saved exactly as it was. New line breaks you type take the form of the line break ending that line.

## Enabling

Inline images are **on by default** in Full Mode; no configuration is needed. They need Markdown notes, so they are off whenever Markdown notes are off. To opt out, set either variable and restart:

```bash
SCRUMBOY_INLINE_IMAGES_ENABLED=0    # images only
SCRUMBOY_MARKDOWN_NOTES_ENABLED=0   # Markdown preview, Mermaid and images
```

`0`, `false`, `off` and `no` disable (case-insensitive); unset or empty keeps the default. Any other value disables images and logs a startup warning. Image files are stored under `DATA_DIR/assets/`, so include that folder in instance-level backups. Projects whose stories embed images export as `.sbz` archives instead of JSON ([backup-and-import.md](backup-and-import.md)).

Restart Scrumboy after changing either value. The database migration is applied automatically at startup; no other configuration is required. See [environment-variables.md](environment-variables.md) for the storage budget variables.

Turning the feature off again never rewrites notes: image Markdown is shown as plain text and the stored files are kept, so re-enabling restores the images.

## Who can add images

| Role | Add images |
|---|---|
| Maintainer | Yes, on any story, and while creating a new story |
| Contributor | Only on stories assigned to them |
| Viewer | No |
| Public visitors | No |

Images can only be added on durable boards. Temporary boards and Anonymous Mode never accept uploads.

## Formats and limits

- **PNG** (including animated **APNG**), **JPEG** and **GIF** (including animation). SVG, WebP, HEIC and other formats are rejected.
- At most **10 MB** per image, 25 megapixels, 16384 px per side, and 300 frames for animations.
- Up to 5 images per paste or drop. Uploads are rate-limited per user.
- Pixels are never re-encoded. Privacy metadata is removed: EXIF, GPS, XMP and IPTC data and PNG text chunks. A JPEG's **orientation** is preserved (as a minimal EXIF segment containing only that value), so photos still display upright. Stored image dimensions are the encoded dimensions.
- APNG frames other than the first are checked structurally on the server but are decoded only by the viewer's browser.

## Storage, budgets and cleanup

Image files live in `DATA_DIR/assets/` (in Docker, inside the existing `/data` volume). Two byte budgets cap how much is stored:

- **Per project**: 1 GiB by default (`SCRUMBOY_ASSET_PROJECT_BUDGET_MB`).
- **Per instance**: 10 GiB by default (`SCRUMBOY_ASSET_INSTANCE_BUDGET_MB`).

An upload or import that would exceed a budget fails with *"Image storage for this board is full"* (HTTP 507). Provision about 1 GiB of disk beyond the instance budget for in-flight uploads, imports and files awaiting cleanup.

Images that no story embeds are cleaned up automatically:

- An image pasted but never saved, or removed from every note, is **deleted after about 7 days**. Retention is finite: an unsaved draft left open for longer than that may lose its images.
- Only an embedded image keeps a file alive. Mentioning its URL in text, inline code, a code block or a link does **not**.
- Archived stories keep their images.
- Deleting a project, or the expiry of a temporary board, removes its images.

After an upgrade from an older Scrumboy version (or a temporary downgrade), Scrumboy re-checks every story on startup before any cleanup runs, so images embedded while an older version was running are never removed.

## Public boards

On a published board, an image is visible to visitors (signed in or not) only when a **non-archived** story of that board embeds it. Unpublishing a board makes its images return "not found" immediately, because responses are marked `private, no-cache`.

## Backups

Project exports that include images download as a `.sbz` archive (a ZIP of the backup JSON plus the image files). Exports without images remain JSON. See [backup-and-import.md](backup-and-import.md). For whole-instance backups, include `DATA_DIR/assets/`. See [recovery.md](recovery.md) for a consistent procedure.

In the Android app, backups that contain images cannot be exported or imported; the app shows a message asking you to use a web browser. See [android.md](android.md).

## Reverse proxies

Proxies must accept request bodies of at least **11 MB** for uploads, and about **520 MB** for `.sbz` imports. For nginx, for example: `client_max_body_size 520m;`.

## Troubleshooting

| Message / status | Meaning |
|---|---|
| "Only PNG, JPEG and GIF images are supported." (400) | The file is not an accepted image, or contains extra data after the image |
| "Image is too large" (413) | Over 10 MB, or a proxy limit was hit |
| "You cannot add images to this story." (403/404) | Your role cannot edit this story's notes |
| "Too many uploads" (429) | Upload rate limit; wait a few minutes |
| "The server is busy" (503) | Too many uploads in progress; retry |
| "Image storage for this board is full" (507) | A storage budget is reached |
| "Another backup archive of yours is still being processed" (429) / busy (503) | `.sbz` imports are processed one per user and two per server; retry shortly |
| "Image unavailable" in preview | The image was deleted, you lack access, or the board is no longer public |
