# Agenda (ICS calendar)

Agenda adds a read-only extra lane on a **durable** board. It shows **today’s** events from subscribed iCalendar (ICS) feeds, in a 24-hour timeline. Cards cannot be edited, dragged, or turned into todos.

Temporary Boards and anonymous boards do not have Agenda.

All members of the project see the same events, timezone, lane name, and lane color.

## Requirements

- Full mode, durable project (open a claimed project board, not `/anon`).
- **`SCRUMBOY_ENCRYPTION_KEY`** — feed URLs are stored encrypted (same key as 2FA / password-reset tokens). Without it, adding a feed fails with “calendar feeds are not configured.” Generate with `openssl rand -base64 32`. See [FAQ](../FAQ.md#how-do-i-generate-scrumboy_encryption_key).
- **Maintainer or higher** to enable Agenda, add or remove feeds, or change timezone / lane name / color. Contributors and viewers can still set their own start-of-day and now-line preferences.

## Set up a board

1. Open the board → **Settings** → **Agenda**.
2. Turn on **Enable Agenda for this board**.
3. Choose the **board timezone**. “Today” and event times use this zone for everyone.
4. Optionally set **lane name** and **lane color** (defaults: `Agenda` and indigo).
5. Under **Add ICS feed**, enter a display name and an **HTTPS** iCalendar URL, then **Add feed**.

Up to **8** feeds per board. Disable a feed without deleting it; use **Refresh** to fetch immediately; **Remove** deletes it.

The URL is stored encrypted and is never shown again. Settings only keeps a hostname preview such as `https://calendar.google.com/…`.

### Which URL to paste

Paste the provider’s **secret or published ICS address**, not a calendar webpage. The file must start with `BEGIN:VCALENDAR`. Share pages and sign-in HTML produce **invalid calendar data**.

| Provider | Where to copy the URL |
| -------- | --------------------- |
| Google Calendar | Calendar settings → Integrate calendar → **Secret address in iCal format** (`https://calendar.google.com/calendar/ical/…/basic.ics`) |
| Outlook | Calendar settings → **Publish a calendar** → ICS link |
| Apple / iCloud | An `https://` calendar URL. `webcal://` is rejected. |

Only `https` is accepted. Userinfo in the URL (`https://user:pass@…`) is rejected. Loopback, private, and link-local destinations are blocked when the server stores or fetches a feed.

## Using the lane

Timed events sit on the hour grid; all-day events sit above it. Each card shows title, start–end time, and location when the feed includes one.

The lane header shows a small host badge per subscribed calendar that has events today (Google, Apple, or a generic calendar icon) plus that calendar’s count.

Agenda is not a workflow column: you cannot drop todos onto it.

### Mobile

On a narrow screen, Agenda is a tab on the left like the other lanes. The tab shows a calendar-days icon and a **single count** of today’s events across every feed (not per calendar). The board name stays in the lane header when you open the tab.

### Personal layout (this browser)

These are **not** project settings:

- **Start of day** — initial scroll position unless an earlier event exists.
- **Prominent now line** — solid red current-time line instead of the dotted line.

They follow the signed-in user (or this browser) via user preferences.

## Refresh

There is no background cron. Feeds are fetched when someone loads the board:

| Age of last snapshot | Behavior |
| -------------------- | -------- |
| Under **15 minutes** | Reuse the cached snapshot |
| 15–30 minutes | Serve the cache, then refresh in the background |
| **30 minutes** or older | Show **Calendar may be out of date**, then refresh in the background |
| Never fetched | Show that the feed has not been fetched yet |

**Refresh** in Settings ignores the 15-minute window and fetches now. A successful fetch notifies open boards (`agenda_updated`). Each fetch waits up to about **10 seconds**.

Typical errors:

| Message | Meaning |
| ------- | ------- |
| invalid calendar data | Body was not usable ICS (HTML page, missing `DTSTART`, bad recurrence, …) |
| calendar feed request failed | HTTP error, DNS, TLS, or empty unexpected response |
| calendar feed timed out | Provider did not answer in time |
| calendar feed too large | Download over **32 MiB** |
| calendar has too many recurring events to process | Recurrence expansion exceeded limits |
| unsupported calendar timezone | ICS timezone the server cannot load |
| calendar feed address is not allowed | Private/blocked destination or disallowed URL |

## Backup

JSON export/import copies Agenda **enabled / timezone / title / color**. **Feed URLs are not exported.** After a JSON restore, add the ICS feeds again.

A full disaster-recovery restore is the instance `DATA_DIR` (SQLite) **together with** `SCRUMBOY_ENCRYPTION_KEY`. Rotating or losing the key makes stored feed URLs unreadable.

## Limits (server)

- 8 ICS feeds per project
- 32 MiB feed body
- 5,000 expanded event instances in the fetch window
- HTTPS only

Scrumboy is not affiliated with, endorsed by, or sponsored by Google LLC, Apple Inc., or Microsoft. Google Calendar, Apple Calendar / iCloud, Outlook, and their logos and marks are trademarks of their respective owners. Host badges in the Agenda lane identify the calendar provider only.
