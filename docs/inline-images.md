# Inline images in story notes

Story notes can contain images: paste a screenshot, drag an image file onto the notes box, or use the **image** button next to the Markdown/Preview tabs. Scrumboy stores the image and inserts Markdown such as:

```markdown
![screenshot](/api/assets/550e8400-e29b-41d4-a716-446655440000)
```

The image renders in the note **Preview** and in the read-only story dialog of a [public board](public-boards.md).

## Enabling

Inline images are **off by default**. They require Full Mode and Markdown notes:

```bash
SCRUMBOY_MARKDOWN_NOTES_ENABLED=1
SCRUMBOY_INLINE_IMAGES_ENABLED=1
```

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
| "Image unavailable" in preview | The image was deleted, you lack access, or the board is no longer public |
