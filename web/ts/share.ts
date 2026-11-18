// The author's Share actions.
//
// The Share screen has a picker and three actions. Copy Text and Card Export use the shared module,
// so they mean the same thing here as they do on the reader's site. Copy Link is different: a draft
// has no public address, so the button is disabled until the work is published.
//
// The works' bodies are not in this page's markup. A small authenticated endpoint supplies the
// selected work on demand, which keeps the page light and means the text is read from the database
// at the moment it is copied rather than from a copy embedded in the HTML.

import { copyText, savePNG, type ShareWork } from "./share_card";

/** The work the endpoint returns. */
interface ShareWorkResponse {
  title: string;
  body: string;
  address: string;
  published: string;
  isPublished: boolean;
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

/** Enables or disables Copy Link for the selected work. */
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

/** Wires the three actions in the current page. */
function init(): void {
  const select = picker();
  if (!select || select.dataset.shareWired === "true") {
    return;
  }
  select.dataset.shareWired = "true";

  select.addEventListener("change", () => syncLinkButton(select));
  syncLinkButton(select);

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
