(() => {
  // web/ts/guest.ts
  var form = document.querySelector(".verse-guest-form");
  var textarea = document.querySelector('textarea[name="content"]');
  var nameField = document.querySelector('input[name="name"]');
  var status = document.querySelector("[data-guest-status]");
  var sendButton = document.querySelector('button[data-guest-send="primary"]');
  var alternativeButton = document.querySelector(
    'button[data-guest-send="alternative"]',
  );
  var changed = form?.getAttribute("data-guest-changed") === "true";
  var closed = false;
  var timer;
  var inflight;
  function report(message) {
    if (status) {
      status.textContent = message;
    }
  }
  function syncButtons() {
    const disabled = closed || !changed;
    if (sendButton) {
      sendButton.disabled = disabled;
    }
    if (alternativeButton) {
      alternativeButton.disabled = disabled;
    }
  }
  async function save(keepalive = false) {
    if (closed || !form || !textarea) {
      return;
    }
    inflight?.abort();
    const controller = new AbortController();
    inflight = controller;
    report("Saving…");
    const body = new FormData();
    const csrf = form.querySelector('input[name="csrf"]');
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
      const data = await response.json();
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
  function scheduleSave() {
    if (timer) {
      clearTimeout(timer);
    }
    changed = true;
    syncButtons();
    timer = setTimeout(() => void save(), 800);
  }
  if (form && textarea) {
    const submission = form.querySelector("[data-guest-submission]");
    if (submission && !submission.value) {
      submission.value =
        typeof crypto.randomUUID === "function"
          ? crypto.randomUUID()
          : "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (char) => {
              const random = Math.floor(Math.random() * 16);
              const digit = char === "x" ? random : (random & 3) | 8;
              return digit.toString(16);
            });
    }
    textarea.addEventListener("input", scheduleSave);
    textarea.addEventListener("blur", () => void save());
    nameField?.addEventListener("change", () => void save());
    window.addEventListener("pagehide", () => {
      if (timer) {
        clearTimeout(timer);
        timer = undefined;
      }
      save(true).catch(() => {
        return;
      });
    });
    const branchField = form.querySelector("[data-guest-branch]");
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
})();
