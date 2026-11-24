// The one first-party bundle on the anonymous public submission page.
//
// The page's two slides are a CSS-only state machine built from native radios, so switching between
// them works with JavaScript disabled. This script does exactly one thing: it wires Copy Text from
// the text the server already rendered. It makes no request, issues no session, and never copies the
// submission capability URL -- that URL is a write capability and must stay in the author's Share
// screen.
//
// The action container is hidden in the markup and revealed only after the listener is attached. A
// script that fails part-way therefore leaves no dead button, and a reader without JavaScript gets
// plain selectable text plus a hint instead of a control that does nothing.

import { copyText } from "./share_card";

// A back/forward cache can restore a page rendered before the work was withdrawn. Reloading puts the
// server's published-and-revocation check back in front of the reader. no-store alone is necessary
// but not sufficient here.
window.addEventListener("pageshow", (event: PageTransitionEvent) => {
  if (event.persisted) {
    window.location.reload();
  }
});

function wireCopy(): void {
  const title = document.querySelector<HTMLElement>("[data-copy-title]");
  const body = document.querySelector<HTMLElement>("[data-copy-body]");
  const button = document.querySelector<HTMLButtonElement>(
    "[data-public-copy-text]",
  );
  const actions = document.querySelector<HTMLElement>("[data-public-actions]");
  const status = document.querySelector<HTMLElement>(
    "[data-public-copy-status]",
  );
  const hint = document.querySelector<HTMLElement>("[data-public-copy-hint]");

  if (!title || !body || !button || !actions || !status) {
    return;
  }

  button.addEventListener("click", async () => {
    // textContent decodes the entities templ escaped, so the clipboard gets the work's original
    // text. Joining explicitly matters: a wrapper's textContent does not insert newlines between
    // its children, and the reader copies the same title + blank line + body shape.
    const text = `${title.textContent ?? ""}\n\n${body.textContent ?? ""}`;
    const copied = await copyText(text);
    status.textContent = copied
      ? "Copied."
      : "Could not copy; select the text and copy it.";
  });

  actions.hidden = false;
  // The hint is only for the no-script reader. Once the button is wired it is noise, so remove it
  // rather than leaving two competing instructions on screen.
  if (hint) {
    hint.hidden = true;
  }
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", wireCopy, { once: true });
} else {
  wireCopy();
}
