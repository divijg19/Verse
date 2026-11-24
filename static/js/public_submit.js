(() => {
  // web/ts/share_card.ts
  async function copyText(text) {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
        return true;
      }
    } catch {}
    const field = document.createElement("textarea");
    field.value = text;
    field.setAttribute("readonly", "");
    field.style.position = "fixed";
    field.style.top = "0";
    field.style.opacity = "0";
    document.body.appendChild(field);
    field.select();
    let copied = false;
    try {
      copied = document.execCommand("copy");
    } catch {
      copied = false;
    }
    field.remove();
    return copied;
  }

  // web/ts/public_submit.ts
  window.addEventListener("pageshow", (event) => {
    if (event.persisted) {
      window.location.reload();
    }
  });
  function wireCopy() {
    const title = document.querySelector("[data-copy-title]");
    const body = document.querySelector("[data-copy-body]");
    const button = document.querySelector("[data-public-copy-text]");
    const actions = document.querySelector("[data-public-actions]");
    const status = document.querySelector("[data-public-copy-status]");
    const hint = document.querySelector("[data-public-copy-hint]");
    if (!title || !body || !button || !actions || !status) {
      return;
    }
    button.addEventListener("click", async () => {
      const text = `${title.textContent ?? ""}

${body.textContent ?? ""}`;
      const copied = await copyText(text);
      status.textContent = copied
        ? "Copied."
        : "Could not copy; select the text and copy it.";
    });
    actions.hidden = false;
    if (hint) {
      hint.hidden = true;
    }
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", wireCopy, { once: true });
  } else {
    wireCopy();
  }
})();
