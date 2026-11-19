// Autosave for a private link's editor.
//
// The guest gets one continuous body for the link, stored server-side. This module never owns the
// text: it only sends what is in the textarea and mirrors the server's answer about whether it
// differs from the last submission. Every decision -- expiry, revocation, validation, change
// detection -- is made by the server, so the plain form still works when this script does not run.

const form = document.querySelector<HTMLFormElement>(".verse-guest-form");
const textarea = document.querySelector<HTMLTextAreaElement>(
  'textarea[name="content"]',
);
const nameField =
  document.querySelector<HTMLInputElement>('input[name="name"]');
const status = document.querySelector<HTMLElement>("[data-guest-status]");
const sendButton = document.querySelector<HTMLButtonElement>(
  'button[data-guest-send="primary"]',
);
const alternativeButton = document.querySelector<HTMLButtonElement>(
  'button[data-guest-send="alternative"]',
);

let changed = form?.getAttribute("data-guest-changed") === "true";
let closed = false;
let timer: ReturnType<typeof setTimeout> | undefined;
let inflight: AbortController | undefined;

function report(message: string): void {
  if (status) {
    status.textContent = message;
  }
}

function syncButtons(): void {
  const disabled = closed || !changed;
  if (sendButton) {
    sendButton.disabled = disabled;
  }
  if (alternativeButton) {
    alternativeButton.disabled = disabled;
  }
}

async function save(keepalive = false): Promise<void> {
  if (closed || !form || !textarea) {
    return;
  }
  inflight?.abort();
  const controller = new AbortController();
  inflight = controller;
  report("Saving…");

  const body = new FormData();
  const csrf = form.querySelector<HTMLInputElement>('input[name="csrf"]');
  if (csrf) {
    body.set("csrf", csrf.value);
  }
  body.set("content", textarea.value);
  body.set("name", nameField?.value ?? "");

  try {
    const response = await fetch("/guest/draft", {
      method: "POST",
      body,
      signal: controller.signal,
      keepalive,
      headers: { accept: "application/json" },
    });
    if (response.status === 404) {
      closed = true;
      report(
        "This link has closed. Your last saved changes are kept with the author only if you sent them.",
      );
      syncButtons();
      return;
    }
    if (!response.ok) {
      report(
        "Could not save this edit. It will be retried when you keep typing.",
      );
      return;
    }
    const data = (await response.json()) as { changed: boolean };
    changed = data.changed;
    report(
      changed
        ? "Saved · ready to send"
        : "Saved · no changes since your last send",
    );
    syncButtons();
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") {
      return;
    }
    report(
      "Could not save this edit. It will be retried when you keep typing.",
    );
  }
}

function scheduleSave(): void {
  if (timer) {
    clearTimeout(timer);
  }
  // An edit is a change until the server says otherwise: this keeps both buttons usable while the
  // debounce is pending, and the server still refuses a submission that is genuinely unchanged.
  changed = true;
  syncButtons();
  timer = setTimeout(() => void save(), 800);
}

if (form && textarea) {
  const submission = form.querySelector<HTMLInputElement>(
    "[data-guest-submission]",
  );
  if (submission && !submission.value) {
    // crypto.randomUUID is unavailable in non-secure contexts, including some staging hosts. A
    // random hex id is enough to make a retried alternative idempotent.
    submission.value =
      typeof crypto.randomUUID === "function"
        ? crypto.randomUUID()
        : "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (char) => {
            const random = Math.floor(Math.random() * 16);
            const digit = char === "x" ? random : (random & 0x3) | 0x8;
            return digit.toString(16);
          });
  }
  textarea.addEventListener("input", scheduleSave);
  textarea.addEventListener("blur", () => void save());
  nameField?.addEventListener("change", () => void save());

  // The debounce window can outlive a tab. A keepalive flush on pagehide catches the last edit;
  // browsers cap keepalive bodies, so a very large draft may still not flush, but the next visit
  // restores everything that was saved before then.
  window.addEventListener("pagehide", () => {
    if (timer) {
      clearTimeout(timer);
      timer = undefined;
    }
    void save(true).catch(() => undefined);
  });

  // A submit button's name/value is only included while the button is enabled. Record the chosen
  // branch in a hidden field first, then disable on the next turn of the event loop so the browser
  // has already collected the form data. Disabling synchronously would silently turn every
  // alternative into a primary submission.
  const branchField = form.querySelector<HTMLInputElement>(
    "[data-guest-branch]",
  );
  for (const button of [sendButton, alternativeButton]) {
    button?.addEventListener("click", () => {
      if (branchField && button.dataset.guestSend) {
        branchField.value = button.dataset.guestSend;
      }
    });
  }
  form.addEventListener("submit", () => {
    setTimeout(() => {
      if (sendButton) {
        sendButton.disabled = true;
      }
      if (alternativeButton) {
        alternativeButton.disabled = true;
      }
    }, 0);
  });
  syncButtons();
}
