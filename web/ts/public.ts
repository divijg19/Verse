import { copyText, savePNG, type ShareWork } from "./share_card";

// The public site's one script.
//
// The public site ships no JavaScript by default: the archive is the complete list of published
// work, and every work has its own page. All of that is in the markup and works with the script
// blocked. What this file adds is convenience on top of it -- narrowing the archive, and copying a
// work's text, address or card. The buttons are hidden until this file has wired them.
//
// It is still one script, because the validator permits exactly one and refuses an inline script or
// a foreign one. The three Share actions use the shared module, so the reader's site and the
// author's private site cannot disagree about what "Copy Text" or "Card Export" means.
//
// A work's body is never put in a second published file. Copy Text and Card Export fetch the work's
// own page and read the .poem element from it. That is a same-origin request to a page the reader
// could open anyway, and it keeps the promise that full body text is not published twice.

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
 * Nothing happens unless the archive's list is on the page, so the front screen and the work pages
 * are untouched by any of this.
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
  const link = row.querySelector<HTMLAnchorElement>("a.share-title");
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

// ---------------------------------------------------------------------------------------------
// Share
// ---------------------------------------------------------------------------------------------

/**
 * Loads a work's body from its own page.
 *
 * The body is not in the search index and is not duplicated anywhere else in the build, so the
 * work page is the single source. Reading it with DOMParser rather than an HTML string means templ's
 * escaping is undone exactly as it would be by the browser, and no text is ever evaluated as markup.
 */
async function loadWork(
  path: string,
  title: string,
  address: string,
  published: string,
): Promise<ShareWork | null> {
  try {
    const response = await fetch(path, { headers: { accept: "text/html" } });
    if (!response.ok) {
      return null;
    }

    const page = new DOMParser().parseFromString(
      await response.text(),
      "text/html",
    );
    const body = page.querySelector(".poem")?.textContent;
    if (body === null || body === undefined) {
      return null;
    }

    return { title, body, address, published };
  } catch {
    return null;
  }
}

/** Reports the outcome beside the button that caused it. */
function report(button: HTMLButtonElement, message: string): void {
  const card = button.closest<HTMLElement>(".share-item");
  const status = card?.querySelector<HTMLElement>(".share-status");
  if (status) {
    status.textContent = message;
  }
}

/** Wires one work's three actions. */
function wireCard(card: HTMLElement): void {
  const title = card.querySelector(".share-title")?.textContent?.trim() ?? "";
  const link = card.querySelector<HTMLAnchorElement>("a.share-title");
  const path = link?.getAttribute("href") ?? "";
  // The absolute address is carried in a data attribute, not shown as page text. The rooted path
  // would be wrong for a reader sharing the work off this site.
  const address = card.dataset.shareAddress ?? "";
  const published = card.querySelector("time")?.getAttribute("datetime") ?? "";

  const actions = card.querySelectorAll<HTMLButtonElement>(
    "button[data-share-action]",
  );
  for (const button of actions) {
    button.addEventListener("click", async () => {
      const action = button.getAttribute("data-share-action");

      if (action === "link") {
        const ok = await copyText(address);
        report(button, ok ? "Address copied." : "Could not copy the address.");
        return;
      }

      // Text and card both need the body, which lives on the work's own page.
      const work = await loadWork(path, title, address, published);
      if (!work) {
        report(button, "The work could not be loaded.");
        return;
      }

      if (action === "text") {
        const ok = await copyText(`${work.title}\n\n${work.body}`);
        report(
          button,
          ok
            ? "Text copied."
            : "Could not copy; open the work to select its text.",
        );
        return;
      }

      if (action === "card") {
        const ok = await savePNG(work);
        report(
          button,
          ok ? "Card saved." : "This browser could not save the card.",
        );
      }
    });
  }

  // Only reveal controls that are already wired. A reader without this script sees no dead buttons.
  const actionsBox = card.querySelector<HTMLElement>(".share-actions");
  if (actionsBox) {
    actionsBox.hidden = false;
  }
}

function wireArchiveActions(): void {
  for (const card of document.querySelectorAll<HTMLElement>(".share-item")) {
    wireCard(card);
  }
}

void wireArchiveActions();
