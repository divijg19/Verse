// The author's Share actions.
//
// The Share screen has a picker and four actions. Copy Text and Card Export use the shared module,
// so they mean the same thing here as they do on the reader's site. Copy Link is different: a draft
// has no public address, so the button is disabled until the work is published. Private Link mints a
// short-lived, one-work session and copies the URL for the author to hand out; a guest's suggestions
// come back through the work's history, where the author can apply them deliberately.
//
// The works' bodies are not in this page's markup. A small authenticated endpoint supplies the
// selected work on demand, which keeps the page light and means the text is read from the database
// at the moment it is copied rather than from a copy embedded in the HTML.

import { copyText, savePNG, type ShareWork } from "./share_card";

/** One private link as the endpoint describes it. */
interface PrivateLinkView {
  id: string;
  poemID: string;
  createdAt: string;
  expiresAt: string;
  revoked: boolean;
  expired: boolean;
  proposalCount: number;
}

/** The work the endpoint returns. */
interface ShareWorkResponse {
  title: string;
  body: string;
  address: string;
  published: string;
  isPublished: boolean;
  links: PrivateLinkView[];
}

/**
 * The Share surface can arrive two ways: as a full page load, or as an HTMX swap into #screen.
 * An HTMX swap does not execute script tags in the swapped markup, so the bundle is loaded once in
 * the layout and re-initialized after each swap.
 *
 * The controls are queried inside the handlers rather than once at module load. At module load the
 * Share page may not exist at all -- the reader can start on any surface -- so a captured element
 * would stay null forever and the actions would never be wired.
 */

/** Finds the picker in the current document. */
function picker(): HTMLSelectElement | null {
  return document.querySelector<HTMLSelectElement>("#share-work-picker");
}

/** Finds the status line in the current document. */
function statusLine(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".verse-share-status");
}

/** Reports an outcome in the page's own status line. */
function report(message: string): void {
  const status = statusLine();
  if (status) {
    status.textContent = message;
  }
}

/** Reads the CSRF token embedded by the Share template. */
function csrf(): string {
  const field = document.querySelector<HTMLInputElement>(
    '.verse-share-panel input[name="csrf"]',
  );
  return field?.value ?? "";
}

/** Reads the selected work from the endpoint. */
async function selectedWork(id: string): Promise<ShareWorkResponse | null> {
  try {
    const response = await fetch(`/share/work/${encodeURIComponent(id)}`, {
      headers: { accept: "application/json" },
    });
    if (!response.ok) {
      return null;
    }
    return (await response.json()) as ShareWorkResponse;
  } catch {
    return null;
  }
}

/** Posts a form to one of the CSRF-protected share endpoints. */
async function postForm(
  path: string,
  fields: Record<string, string>,
): Promise<Response | null> {
  const body = new FormData();
  body.set("csrf", csrf());
  for (const [key, value] of Object.entries(fields)) {
    body.set(key, value);
  }
  try {
    return await fetch(path, { method: "POST", body });
  } catch {
    return null;
  }
}

/** Formats an expiry as a short, local, readable instant. */
function formatTime(raw: string): string {
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) {
    return "unknown";
  }
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

/** Draws the selected work's private links. Elements are built, never parsed from HTML. */
function renderLinks(links: PrivateLinkView[]): void {
  const section = document.querySelector<HTMLElement>("[data-private-links]");
  const list = document.querySelector<HTMLElement>("[data-private-link-list]");
  if (!section || !list) {
    return;
  }
  section.hidden = false;
  list.replaceChildren();

  if (links.length === 0) {
    const empty = document.createElement("p");
    empty.className = "verse-share-links-empty";
    empty.textContent = "No private links for this work.";
    list.appendChild(empty);
    return;
  }

  for (const link of links) {
    const row = document.createElement("div");
    row.className = "verse-share-link-row";

    const details = document.createElement("div");
    const summary = document.createElement("p");
    const state = link.revoked
      ? "Revoked"
      : link.expired
        ? "Expired"
        : link.proposalCount > 0
          ? `${link.proposalCount} suggestion${link.proposalCount === 1 ? "" : "s"} received`
          : "Active";
    summary.textContent = state;
    const times = document.createElement("small");
    times.textContent = `Created ${formatTime(link.createdAt)} · closes ${formatTime(link.expiresAt)}`;
    details.append(summary, times);

    const revoke = document.createElement("button");
    revoke.type = "button";
    revoke.className = "verse-share-link-revoke";
    revoke.textContent = "Revoke";
    revoke.dataset.revoke = link.id;
    revoke.disabled = link.revoked || link.expired;
    row.append(details, revoke);
    list.appendChild(row);
  }
}

/** Refreshes the link list for the work currently selected. */
async function refreshLinks(id: string): Promise<void> {
  const data = await selectedWork(id);
  if (data) {
    renderLinks(data.links ?? []);
  }
}

/** Enabled or disables Copy Link for the selected work. */
function syncLinkButton(select: HTMLSelectElement): void {
  const button = document.querySelector<HTMLButtonElement>(
    'button[data-share-action="link"]',
  );
  if (!button) {
    return;
  }

  const option = select.selectedOptions[0];
  const published = option?.getAttribute("data-published") === "true";

  button.disabled = !published;
  button.title = published ? "" : "Publish this work first";

  const hint = document.querySelector<HTMLElement>(".verse-share-link-hint");
  if (hint) {
    hint.hidden = published;
  }

  const status = statusLine();
  if (status) {
    status.textContent = "";
  }
}

/** Wires the four actions in the current page. */
function init(): void {
  const select = picker();
  if (!select || select.dataset.shareWired === "true") {
    return;
  }
  select.dataset.shareWired = "true";

  select.addEventListener("change", () => {
    syncLinkButton(select);
    if (select.value) {
      void refreshLinks(select.value);
    }
  });
  syncLinkButton(select);
  if (select.value) {
    void refreshLinks(select.value);
  }

  // Revocation is delegated because the buttons are created after init runs. The marker prevents a
  // second listener after an HTMX swap, which would revoke the same link twice.
  if (document.body.dataset.revokeWired !== "true") {
    document.body.dataset.revokeWired = "true";
    document.body.addEventListener("click", async (event) => {
      const target = event.target;
      if (!(target instanceof HTMLButtonElement)) {
        return;
      }
      const linkID = target.dataset.revoke;
      if (!linkID) {
        return;
      }
      const current = picker();
      const workID = current?.value ?? "";
      const response = await postForm("/share/link/revoke", {
        id: workID,
        link: linkID,
      });
      if (response?.ok) {
        report("Link revoked.");
        await refreshLinks(workID);
      } else {
        report("Could not revoke that link.");
      }
    });
  }

  for (const button of document.querySelectorAll<HTMLButtonElement>(
    "button[data-share-action]",
  )) {
    button.addEventListener("click", async () => {
      const action = button.getAttribute("data-share-action");
      const id = select.value;

      if (!id) {
        report("Choose a work first.");
        return;
      }

      if (action === "private") {
        const response = await postForm("/share/link", { id });
        if (!response?.ok) {
          report("Could not create a private link.");
          return;
        }
        const data = (await response.json()) as { url: string };
        const ok = await copyText(data.url);
        report(ok ? "Private link copied." : "Could not copy the link.");
        await refreshLinks(id);
        return;
      }

      const data = await selectedWork(id);
      if (!data) {
        report("The work could not be loaded.");
        return;
      }

      if (action === "text") {
        const ok = await copyText(`${data.title}\n\n${data.body}`);
        report(
          ok ? "Text copied." : "Could not copy this browser's clipboard.",
        );
        return;
      }

      if (action === "link") {
        if (!data.isPublished) {
          report("Publish this work first.");
          return;
        }
        if (!data.address) {
          report("The public address is not configured.");
          return;
        }
        const ok = await copyText(data.address);
        report(
          ok ? "Address copied." : "Could not copy this browser's clipboard.",
        );
        return;
      }

      if (action === "card") {
        const work: ShareWork = {
          title: data.title,
          body: data.body,
          address: data.address,
          published: data.published,
        };
        const ok = await savePNG(work);
        report(ok ? "Card saved." : "This browser could not save the card.");
      }
    });
  }
}

document.addEventListener("DOMContentLoaded", init);
document.body.addEventListener("htmx:afterSwap", init);
