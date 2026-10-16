# Changes

- Added case-insensitive sprint discovery to authenticated board Omni search using the existing slug-scoped sprint vocabulary.
- Added distinct Sprint suggestions, duplicate-name number labels, numeric deep-link fallback labels, and removable selected-sprint pills on desktop and mobile.
- Sprint selection now writes the project-local sprint number, cancels pending text search, clears the search term, preserves other filters, and reloads once.
- Made sprint vocabulary refreshes update Omni state safely while rejecting stale and cross-project responses.
- Regenerated the required frontend distribution files and added focused unit and browser regressions.

# Verification

- `npx tsc --noEmit`
- `npm test` (199 files, 3,001 tests)
- `npm run build`
- Targeted Playwright: member Omni Sprint filtering (1 passed)

# Remaining issues

None.
