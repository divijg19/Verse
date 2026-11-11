// The archive's search box.
//
// The public site ships no JavaScript by default, and the archive page is the complete list of
// published work whether this file loads or not. That is the fallback: with the script blocked, or
// with the index unreachable, the reader still has every work.
//
// What this file adds is a way to narrow that list without a round trip. The index it reads is
// search.json, which the publisher already writes and which carries an id, a path, a title, a date
// and a bounded excerpt for each published work -- and nothing else. The full text of a work is not
// published twice.
//
// The box is inserted by this script rather than being in the markup, because a search input that
// cannot search is worse than no search input: a reader who cannot type into it has no way to tell
// that the page is not broken. A page with no script has no box at all.
//
// The reader's own navigation and the site's design are untouched by any of this. Nothing here
// changes what is published or how a work is rendered; it only hides list entries that do not match.

/** One entry in the search index. */
interface VerseSearchEntry {
  /** The work's stable identifier. */
  id: string;

  /** The work's public path, which is also the link the list row carries. */
  path: string;

  /** The work's title. */
  title: string;

  /** The work's publication date, as an ISO day. */
  published: string;

  /** A bounded, plain-text summary of the work. */
  excerpt: string;
}

/** The search index, as written by the publisher. */
interface VerseSearchIndex {
  /** The index's shape version. */
  version: number;

  /** One entry per published work. */
  entries: VerseSearchEntry[];
}

/** Where the index is published. */
const indexURL = "/search.json";

/**
 * The list this box filters.
 *
 * Nothing happens unless the archive's list is on the page, so the front screen and the share
 * screen are untouched by any of this.
 */
const list = document.querySelector<HTMLOListElement>(".work-list");

/** Reads the index and returns it, or null when it cannot be read. */
async function readIndex(): Promise<VerseSearchIndex | null> {
  try {
    const response = await fetch(indexURL, {
      headers: { accept: "application/json" },
    });
    if (!response.ok) {
      return null;
    }
    const body: unknown = await response.json();
    if (typeof body !== "object" || body === null) {
      return null;
    }
    const index = body as VerseSearchIndex;
    if (!Array.isArray(index.entries)) {
      return null;
    }
    return index;
  } catch {
    return null;
  }
}

/**
 * The index entry a row belongs to, or null when the row is not in the index.
 *
 * The row's own path is what identifies it. The index is keyed by path because that is the one
 * thing both sides know about.
 */
function entryFor(
  row: HTMLLIElement,
  entries: Map<string, VerseSearchEntry>,
): VerseSearchEntry | null {
  const link = row.querySelector<HTMLAnchorElement>("a");
  const path = link ? link.getAttribute("href") : null;
  return path ? (entries.get(path) ?? null) : null;
}

/**
 * The text a row is matched against.
 *
 * The title and the excerpt, so that a reader can find a work by a phrase inside it as well as by
 * its name.
 */
function rowText(
  row: HTMLLIElement,
  entries: Map<string, VerseSearchEntry>,
): string {
  const entry = entryFor(row, entries);
  const title = row.querySelector(".work-title")?.textContent ?? "";
  return `${title} ${entry?.excerpt ?? ""}`.toLowerCase();
}

/**
 * Marks the matched phrase in a row's excerpt.
 *
 * The excerpt is rewritten from the index rather than from the element's own text, so the highlight
 * is a function of the index entry and the search term and nothing else. That makes it idempotent:
 * searching again replaces the previous highlight rather than nesting another one inside it.
 *
 * Every occurrence is marked, not just the first. A reader who searched for a common word wants to
 * see all of them.
 */
function highlight(
  row: HTMLLIElement,
  entry: VerseSearchEntry | null,
  needle: string,
): void {
  const element = row.querySelector<HTMLElement>(".work-excerpt");
  if (!element || !entry) {
    return;
  }

  const plain = entry.excerpt;
  if (needle === "") {
    element.replaceChildren(document.createTextNode(plain));
    return;
  }

  const haystack = plain.toLowerCase();
  const parts: Node[] = [];
  let rest = plain;
  let restLower = haystack;

  for (;;) {
    const at = restLower.indexOf(needle);
    if (at < 0) {
      break;
    }

    if (at > 0) {
      parts.push(document.createTextNode(rest.slice(0, at)));
    }

    const mark = document.createElement("mark");
    mark.textContent = rest.slice(at, at + needle.length);
    parts.push(mark);

    rest = rest.slice(at + needle.length);
    restLower = restLower.slice(at + needle.length);
  }

  if (parts.length === 0) {
    // The phrase matched the title rather than the excerpt, so there is nothing in the excerpt to
    // mark. The row's text is left as the index has it.
    element.replaceChildren(document.createTextNode(plain));
    return;
  }

  if (rest.length > 0) {
    parts.push(document.createTextNode(rest));
  }

  element.replaceChildren(...parts);
}

/** Shows the rows that match and hides the rest. */
function applyFilter(
  rows: HTMLLIElement[],
  needle: string,
  entries: Map<string, VerseSearchEntry>,
): number {
  let shown = 0;
  for (const row of rows) {
    const matches = needle === "" || rowText(row, entries).includes(needle);
    row.hidden = !matches;

    // Every row is re-highlighted, not only the ones that match. A row that matched the last
    // search and does not match this one would otherwise keep the old highlight, and it would
    // still be there when the search is cleared.
    highlight(row, entryFor(row, entries), needle);

    if (matches) {
      shown += 1;
    }
  }
  return shown;
}

async function main(): Promise<void> {
  if (!list) {
    return;
  }

  const rows = Array.from(list.querySelectorAll<HTMLLIElement>("li"));
  if (rows.length === 0) {
    return;
  }

  const index = await readIndex();
  if (!index) {
    return;
  }

  const entries = new Map<string, VerseSearchEntry>();
  for (const entry of index.entries) {
    entries.set(entry.path, entry);
  }

  const box = document.createElement("input");
  box.type = "search";
  box.className = "archive-search";
  box.placeholder = "Search the archive";
  box.setAttribute("aria-label", "Search published work by title or phrase");
  box.autocomplete = "off";

  const status = document.createElement("p");
  status.className = "archive-search-status";
  status.setAttribute("aria-live", "polite");

  const field = document.createElement("div");
  field.className = "archive-search-field";
  field.append(box, status);

  list.parentElement?.insertBefore(field, list);

  const total = rows.length;

  const update = (): void => {
    const needle = box.value.trim().toLowerCase();
    const shown = applyFilter(rows, needle, entries);

    if (needle === "") {
      // The count above the list already says how many works there are, and it says it in the
      // page's own words rather than in a numeral. This script only reports what the count cannot:
      // how many of them are on screen right now.
      status.textContent = "";
      return;
    }

    status.textContent =
      shown === 0
        ? `Nothing matches “${box.value.trim()}”.`
        : `${shown} of ${total} shown`;
  };

  box.addEventListener("input", update);
  update();
}

void main();
