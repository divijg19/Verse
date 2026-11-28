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
import { lockBodyScroll, unlockBodyScroll } from "./scroll_lock";

/** States are decided by the server; the browser never infers them from an empty URL. */
type LinkState =
  "active" | "unrecoverable" | "expired" | "revoked" | "unpublished";

const endedStates: LinkState[] = ["expired", "revoked"];

function isEnded(state: LinkState): boolean {
  return endedStates.includes(state);
}

function stateLabel(state: LinkState): string {
  switch (state) {
    case "unrecoverable":
      return "Active · URL not recoverable";
    case "expired":
      return "Expired";
    case "revoked":
      return "Revoked";
    case "unpublished":
      return "Unpublished";
    default:
      return "Active";
  }
}

/** The controls a row may offer. Absent means the action cannot succeed, so it is not rendered. */
function rowControls(
  state: LinkState,
  kind: "private" | "public",
): { copy: boolean; fresh: boolean; rotate: boolean; revoke: boolean } {
  const ended = isEnded(state);
  return {
    copy: state === "active",
    fresh: kind === "private" && !ended,
    // A revoked public row needs Rotate: it is the only repair path after the primary action 409s.
    rotate: kind === "public" && state !== "unpublished",
    revoke: !ended && state !== "unpublished",
  };
}

/** One private link as the endpoint describes it. */
interface PrivateLinkView {
  id: string;
  poemID: string;
  createdAt: string;
  expiresAt: string;
  proposalCount: number;
  state: LinkState;
  copyable: boolean;
  /** Present only when the server can supply a working capability URL. */
  url: string;
}

interface PublicLinkView {
  id: string;
  poemID: string;
  createdAt: string;
  submissionCount: number;
  state: LinkState;
  copyable: boolean;
  /** Present only when the server can supply a working capability URL. */
  url: string;
}

/** One read-only public suggestion, newest first, bounded by shareSubmissionLimit. */
interface PublicSubmission {
  id: string;
  linkID: string;
  guestName: string;
  content: string;
  createdAt: string;
}

/** The work the endpoint returns. Suggestions are read only when the overlay opens. */
interface ShareWorkResponse {
  title: string;
  body: string;
  address: string;
  published: string;
  isPublished: boolean;
  publicAddressConfigured: boolean;
  links: PrivateLinkView[];
  publicLinks: PublicLinkView[];
}

/** The overlay's own payload, including an explicit flag so a failed read is never called empty. */
interface ShareSubmissionsResponse {
  submissions: PublicSubmission[];
  total: number;
  unavailable: boolean;
}

/** The opener that should receive focus again when the overlay closes. */
let suggestionsOpener: HTMLButtonElement | null = null;

/** The counted body lock is module state because an HTMX swap can remove the dialog before close. */
let suggestionsLocked = false;

/**
 * The Share surface can arrive two ways: as a full page load, or as an HTMX swap into #screen.
 * The vendored htmx re-inserts script tags from swapped markup, so this module is loaded from the
 * layout and re-initialized after each swap rather than relying on a script in the fragment.
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

/** Reads one work's suggestions only when the overlay is opened. */
async function fetchSubmissions(
  id: string,
): Promise<ShareSubmissionsResponse | null> {
  try {
    const response = await fetch(
      `/share/work/${encodeURIComponent(id)}/submissions`,
      { headers: { accept: "application/json" } },
    );
    if (!response.ok) {
      return null;
    }
    return (await response.json()) as ShareSubmissionsResponse;
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

// Copy URLs come from the Share payload, not from a page-session cache. The server derives each
// active link's URL from a stored seed, so a row minted in an earlier session still has a working
// Copy button after reload. A row whose token cannot be recovered simply has no URL and no Copy.

/** Builds only the controls this row may use, with Revoke rightmost when it is present. */
function linkButtons(
  id: string,
  copyAttr: string,
  revokeAttr: string,
  controls: { copy: boolean; fresh: boolean; rotate: boolean; revoke: boolean },
  url: string,
  newAttr: string | null = null,
  rotateAttr: string | null = null,
): HTMLElement {
  const group = document.createElement("div");
  group.className = "verse-share-link-buttons";

  if (controls.copy && url) {
    const copy = document.createElement("button");
    copy.type = "button";
    copy.className = "verse-share-link-copy";
    copy.textContent = "Copy";
    copy.setAttribute(copyAttr, id);
    copy.setAttribute("data-copy-url", url);
    group.appendChild(copy);
  }

  if (controls.fresh && newAttr) {
    const fresh = document.createElement("button");
    fresh.type = "button";
    fresh.className = "verse-share-link-copy";
    fresh.textContent = "New";
    fresh.setAttribute(newAttr, id);
    group.appendChild(fresh);
  }
  if (controls.rotate && rotateAttr) {
    const rotate = document.createElement("button");
    rotate.type = "button";
    rotate.className = "verse-share-link-copy";
    rotate.textContent = "Rotate";
    rotate.setAttribute(rotateAttr, id);
    group.appendChild(rotate);
  }

  if (controls.revoke) {
    const revoke = document.createElement("button");
    revoke.type = "button";
    revoke.className = "verse-share-link-revoke";
    revoke.textContent = "Revoke";
    revoke.setAttribute(revokeAttr, id);
    group.appendChild(revoke);
  }
  return group;
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

/** The suggestions dialog, found at use time because an HTMX swap replaces the surface. */
function suggestionsDialog(): HTMLDialogElement | null {
  return document.querySelector<HTMLDialogElement>(
    "[data-suggestions-overlay]",
  );
}

/** Closes the overlay if it is open. Every lifecycle path goes through the dialog's close event. */
function closeSuggestions(): void {
  const dialog = suggestionsDialog();
  if (dialog?.open) {
    dialog.close();
  }
}

/** Draws one bubble per submission. Bubbles are read-only text, never parsed HTML. */
function renderSuggestions(
  submissions: PublicSubmission[],
  total: number,
  unavailable: boolean,
): void {
  const list = document.querySelector<HTMLElement>("[data-suggestions-list]");
  const status = document.querySelector<HTMLElement>(
    "[data-suggestions-status]",
  );
  if (!list || !status) {
    return;
  }
  list.replaceChildren();
  if (submissions.length === 0) {
    const empty = document.createElement("p");
    empty.className = "verse-suggestions-empty";
    // The endpoint says explicitly when a read failed; a total is not enough because the count
    // read can fail in the same incident.
    empty.textContent = unavailable
      ? "Suggestions are temporarily unavailable."
      : "No suggestions for this work yet.";
    list.appendChild(empty);
    status.textContent = "";
    return;
  }
  for (const submission of submissions) {
    const bubble = document.createElement("article");
    bubble.className = "verse-suggestion-bubble";
    const meta = document.createElement("p");
    meta.className = "verse-suggestion-meta";
    meta.textContent = `${submission.guestName || "Anonymous"} · ${formatTime(submission.createdAt)}`;
    const text = document.createElement("pre");
    text.className = "verse-suggestion-text";
    text.textContent = submission.content;
    bubble.append(meta, text);
    list.appendChild(bubble);
  }
  // A count-only failure leaves the list usable but makes its bound unknowable. Say so rather than
  // letting the newest slice look like the whole record.
  if (unavailable) {
    status.textContent =
      "Suggestions are temporarily unavailable; showing what could be read.";
  } else if (total > submissions.length) {
    status.textContent = `Showing the ${submissions.length} most recent of ${total} suggestions.`;
  } else {
    status.textContent = "";
  }
}

/** Wires the dialog's close and backdrop paths once per mounted surface. */
function wireSuggestions(): void {
  const dialog = suggestionsDialog();
  if (!dialog || dialog.dataset.suggestionsWired === "true") {
    return;
  }
  // A swap replaces an open dialog, so its close event never fires on this new element. Release
  // the lock here instead of leaving the page unscrollable.
  if (suggestionsLocked && !dialog.open) {
    unlockBodyScroll();
    suggestionsLocked = false;
  }
  dialog.dataset.suggestionsWired = "true";
  dialog.addEventListener("close", () => {
    if (suggestionsLocked) {
      unlockBodyScroll();
      suggestionsLocked = false;
    }
    const opener = suggestionsOpener;
    suggestionsOpener = null;
    if (opener?.isConnected) {
      opener.focus();
    }
  });
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog) {
      dialog.close();
    }
  });
}

/** Fetches fresh suggestions on open so a withdrawn work or revoked link cannot show cached text. */
async function openSuggestions(opener: HTMLButtonElement): Promise<void> {
  const dialog = suggestionsDialog();
  if (!dialog || dialog.open) {
    return;
  }
  const workID = picker()?.value ?? "";
  if (!workID) {
    return;
  }
  suggestionsOpener = opener;
  const data = await fetchSubmissions(workID);
  // An HTMX swap during the request replaces the dialog. Never show another work's text in a
  // detached element, and never clear an opener that a later open already claimed.
  const current = suggestionsDialog();
  if (
    (picker()?.value ?? "") !== workID ||
    !current ||
    current !== dialog ||
    !current.isConnected ||
    current.open
  ) {
    if (suggestionsOpener === opener) {
      suggestionsOpener = null;
    }
    return;
  }
  renderSuggestions(
    data?.submissions ?? [],
    data?.total ?? 0,
    !data || data.unavailable,
  );
  current.showModal();
  lockBodyScroll();
  suggestionsLocked = true;
  requestAnimationFrame(() => {
    current
      .querySelector<HTMLButtonElement>("[data-suggestions-close]")
      ?.focus();
  });
}

/** Draws the selected work's private links. Elements are built, never parsed from HTML. */
function renderLinks(links: PrivateLinkView[]): void {
  const section = document.querySelector<HTMLElement>("[data-links]");
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

  // Stable sort preserves the service's newest-first order inside each group.
  const ordered = [...links].sort(
    (a, b) => Number(isEnded(a.state)) - Number(isEnded(b.state)),
  );
  for (const link of ordered) {
    const row = document.createElement("div");
    row.className = isEnded(link.state)
      ? "verse-share-link-row verse-share-link-row-ended"
      : "verse-share-link-row";

    const details = document.createElement("div");
    const summary = document.createElement("p");
    const count =
      link.proposalCount > 0
        ? ` · ${link.proposalCount} submission branch${link.proposalCount === 1 ? "" : "es"}`
        : "";
    summary.textContent = `Private · ${stateLabel(link.state)}${count}`;
    const times = document.createElement("small");
    const boundary = isEnded(link.state) ? "closed" : "closes";
    times.textContent = `Created ${formatTime(link.createdAt)} · ${boundary} ${formatTime(link.expiresAt)}`;
    details.append(summary, times);

    row.append(
      details,
      linkButtons(
        link.id,
        "data-copy",
        "data-revoke",
        rowControls(link.state, "private"),
        link.url ?? "",
        "data-new",
      ),
    );
    list.appendChild(row);
  }
}

/** Draws the author's public submission links. */
function renderPublicLinks(links: PublicLinkView[]): void {
  const section = document.querySelector<HTMLElement>("[data-links]");
  const list = document.querySelector<HTMLElement>("[data-public-link-list]");
  if (!section || !list) return;
  section.hidden = false;
  list.replaceChildren();
  if (links.length === 0) {
    const empty = document.createElement("p");
    empty.className = "verse-share-links-empty";
    empty.textContent = "No public links for this work.";
    list.appendChild(empty);
    return;
  }
  const ordered = [...links].sort(
    (a, b) => Number(isEnded(a.state)) - Number(isEnded(b.state)),
  );
  for (const link of ordered) {
    const row = document.createElement("div");
    row.className = isEnded(link.state)
      ? "verse-share-link-row verse-share-link-row-ended"
      : "verse-share-link-row";
    const details = document.createElement("div");
    const summary = document.createElement("p");
    const count =
      link.submissionCount > 0
        ? ` · ${link.submissionCount} submission${link.submissionCount === 1 ? "" : "s"}`
        : "";
    summary.textContent = `Public · ${stateLabel(link.state)}${count}`;
    const times = document.createElement("small");
    times.textContent = `Created ${formatTime(link.createdAt)}`;
    details.append(summary, times);
    const open = document.createElement("button");
    open.type = "button";
    open.className = "verse-share-link-open";
    // The overlay shows the newest public submissions for the whole work, bounded to the most
    // recent ones, so the accessible name must be work-wide without promising completeness.
    open.dataset.suggestionsOpen = "";
    open.textContent = "View suggestions";
    open.setAttribute("aria-label", "View recent suggestions for this work");
    row.append(
      details,
      linkButtons(
        link.id,
        "data-public-copy",
        "data-public-revoke",
        rowControls(link.state, "public"),
        link.url ?? "",
        null,
        "data-public-rotate",
      ),
      open,
    );
    list.appendChild(row);
  }
}

/** Refreshes the link list for the work currently selected. */
async function refreshLinks(id: string): Promise<void> {
  // Rows are rebuilt here, so the opener the dialog would restore focus to is about to disappear.
  closeSuggestions();
  const data = await selectedWork(id);
  if (!data) {
    // A work can be deleted while its Share screen is open. Clear the stale rows rather than
    // leaving capabilities on screen for a work the server no longer serves.
    for (const selector of [
      "[data-private-link-list]",
      "[data-public-link-list]",
    ]) {
      const list = document.querySelector<HTMLElement>(selector);
      if (!list) continue;
      list.replaceChildren();
      const empty = document.createElement("p");
      empty.className = "verse-share-links-empty";
      empty.textContent = "This work is no longer available.";
      list.appendChild(empty);
    }
    report("This work is no longer available.");
    return;
  }
  renderLinks(data.links ?? []);
  renderPublicLinks(data.publicLinks ?? []);
}

/** Enables or disables the two actions that depend on publication and reader-origin config. */
function syncLinkButton(select: HTMLSelectElement): void {
  const option = select.selectedOptions[0];
  const published = option?.getAttribute("data-published") === "true";
  const configured =
    document
      .querySelector<HTMLElement>("[data-public-address-configured]")
      ?.getAttribute("data-public-address-configured") === "true";

  for (const button of document.querySelectorAll<HTMLButtonElement>(
    'button[data-share-action="link"], button[data-share-action="public"]',
  )) {
    const action = button.getAttribute("data-share-action");
    button.disabled = !published || (action === "link" && !configured);
    button.title = !published
      ? "Publish this work first"
      : action === "link" && !configured
        ? "Set PUBLIC_BASE_URL to the reader origin"
        : "";
  }

  // The hint explains both disabled reasons, so a published work with no reader origin is not left
  // with a dead button and no explanation.
  const hint = document.querySelector<HTMLElement>(".verse-share-link-hint");
  if (hint) {
    hint.hidden = published && configured;
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
  wireSuggestions();

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
      if (target.dataset.suggestionsClose !== undefined) {
        closeSuggestions();
        return;
      }
      if (target.dataset.suggestionsOpen !== undefined) {
        void openSuggestions(target);
        return;
      }
      const freshID = target.dataset.new;
      if (freshID) {
        const current = picker();
        const workID = current?.value ?? "";
        const response = await postForm("/share/link/new", { id: workID });
        if (!response?.ok) {
          report("Could not create a new private link.");
          return;
        }
        const data = (await response.json()) as { url: string };
        const ok = await copyText(data.url);
        report(
          ok
            ? "New private link copied."
            : "New link created, but the clipboard is unavailable.",
        );
        await refreshLinks(workID);
        return;
      }
      const rotateID = target.dataset.publicRotate;
      if (rotateID) {
        const current = picker();
        const workID = current?.value ?? "";
        const response = await postForm("/share/public-link/rotate", {
          id: workID,
          link: rotateID,
        });
        if (!response?.ok) {
          report("Could not rotate that public link.");
          return;
        }
        const data = (await response.json()) as { url: string };
        const ok = await copyText(data.url);
        report(
          ok
            ? "Public link rotated and copied. The previous URL no longer works."
            : "Public link rotated, but the clipboard is unavailable.",
        );
        await refreshLinks(workID);
        return;
      }
      const copyID = target.dataset.copy ?? target.dataset.publicCopy;
      if (copyID) {
        const url = target.dataset.copyUrl;
        if (url) {
          void copyText(url).then((ok) =>
            report(
              ok ? "Link copied." : "Could not copy this browser's clipboard.",
            ),
          );
        }
        return;
      }
      const linkID = target.dataset.revoke;
      const publicID = target.dataset.publicRevoke;
      if (!linkID && !publicID) {
        return;
      }
      const current = picker();
      const workID = current?.value ?? "";
      const response = await postForm(
        publicID ? "/share/public-link/revoke" : "/share/link/revoke",
        { id: workID, link: publicID ?? linkID ?? "" },
      );
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

      if (action === "public") {
        const response = await postForm("/share/public-link", { id });
        if (!response?.ok) {
          report(
            response?.status === 409
              ? "Publish the work first, or use Rotate on an existing public link."
              : "Could not create a public link.",
          );
          return;
        }
        const data = (await response.json()) as {
          id: string;
          url: string;
          created: boolean;
        };
        const ok = await copyText(data.url);
        report(
          ok
            ? data.created
              ? "Public link created and copied."
              : "Existing public link copied."
            : "Could not copy the link.",
        );
        await refreshLinks(id);
        return;
      }

      if (action === "private") {
        const response = await postForm("/share/link", { id });
        if (!response?.ok) {
          report("Could not create a private link.");
          return;
        }
        const data = (await response.json()) as {
          id: string;
          url: string;
          created: boolean;
        };
        const ok = await copyText(data.url);
        report(
          ok
            ? data.created
              ? "Private link created and copied."
              : "Existing private link copied."
            : "Could not copy the link.",
        );
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
          report(
            data.publicAddressConfigured
              ? "This work has no public address yet."
              : "Set PUBLIC_BASE_URL to the reader origin to copy a public link.",
          );
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

// A swap away from Share removes the dialog without firing its close event. Release the counted
// body lock here; the boolean makes a later close event on the detached element a no-op.
document.body.addEventListener("htmx:afterSwap", () => {
  if (
    suggestionsLocked &&
    !document.querySelector("[data-suggestions-overlay]")
  ) {
    unlockBodyScroll();
    suggestionsLocked = false;
  }
});
