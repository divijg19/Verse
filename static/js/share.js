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
  var titleFont = 'italic 44px Georgia, Cambria, "Times New Roman", serif';
  var bodyFont = '28px Georgia, Cambria, "Times New Roman", serif';
  var metaFont = "18px ui-sans-serif, system-ui, sans-serif";
  function wrap(ctx, text, maxWidth) {
    const lines = [];
    for (const paragraph of text.split(`
`)) {
      if (paragraph.trim() === "") {
        lines.push("");
        continue;
      }
      let line = "";
      for (const word of paragraph.split(/\s+/).filter(Boolean)) {
        const candidate = line === "" ? word : `${line} ${word}`;
        if (ctx.measureText(candidate).width <= maxWidth) {
          line = candidate;
          continue;
        }
        if (line !== "") {
          lines.push(line);
          line = "";
        }
        if (ctx.measureText(word).width <= maxWidth) {
          line = word;
          continue;
        }
        let fragment = "";
        for (const character of word) {
          if (
            ctx.measureText(fragment + character).width > maxWidth &&
            fragment
          ) {
            lines.push(fragment);
            fragment = "";
          }
          fragment += character;
        }
        line = fragment;
      }
      if (line !== "") {
        lines.push(line);
      }
    }
    return lines;
  }
  function renderCard(work) {
    const canvas = document.createElement("canvas");
    canvas.width = 1200;
    canvas.height = 630;
    const ctx = canvas.getContext("2d");
    if (!ctx) {
      throw new Error("this browser cannot draw a card");
    }
    ctx.fillStyle = "#0f0f12";
    ctx.fillRect(0, 0, canvas.width, canvas.height);
    const inset = 96;
    const width = canvas.width - inset * 2;
    ctx.fillStyle = "#c9a7ff";
    ctx.font = metaFont;
    ctx.fillText("VERSE", inset, inset + 6);
    ctx.fillStyle = "#f2f0ec";
    ctx.font = titleFont;
    const titleLines = wrap(ctx, work.title || "Untitled", width).slice(0, 2);
    let y = inset + 88;
    for (const line of titleLines) {
      ctx.fillText(line, inset, y);
      y += 58;
    }
    y += 18;
    ctx.fillStyle = "#cfcdc8";
    ctx.font = bodyFont;
    const bodyLines = wrap(ctx, work.body, width);
    const room = Math.max(0, Math.floor((canvas.height - inset - y - 90) / 42));
    for (const line of bodyLines.slice(0, room)) {
      ctx.fillText(line, inset, y);
      y += 42;
    }
    ctx.fillStyle = "#9a968f";
    ctx.font = metaFont;
    const footer = work.published || work.address;
    ctx.fillText(footer.slice(0, 90), inset, canvas.height - inset);
    return canvas;
  }
  function savePNG(work) {
    return new Promise((resolve) => {
      renderCard(work).toBlob((blob) => {
        if (!blob) {
          resolve(false);
          return;
        }
        const url = URL.createObjectURL(blob);
        const link = document.createElement("a");
        link.href = url;
        link.download = "verse-card.png";
        document.body.appendChild(link);
        link.click();
        link.remove();
        queueMicrotask(() => URL.revokeObjectURL(url));
        resolve(true);
      }, "image/png");
    });
  }

  // web/ts/share.ts
  function picker() {
    return document.querySelector("#share-work-picker");
  }
  function statusLine() {
    return document.querySelector(".verse-share-status");
  }
  function report(message) {
    const status = statusLine();
    if (status) {
      status.textContent = message;
    }
  }
  function csrf() {
    const field = document.querySelector(
      '.verse-share-panel input[name="csrf"]',
    );
    return field?.value ?? "";
  }
  async function selectedWork(id) {
    try {
      const response = await fetch(`/share/work/${encodeURIComponent(id)}`, {
        headers: { accept: "application/json" },
      });
      if (!response.ok) {
        return null;
      }
      return await response.json();
    } catch {
      return null;
    }
  }
  async function postForm(path, fields) {
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
  function formatTime(raw) {
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
  function renderLinks(links) {
    const section = document.querySelector("[data-private-links]");
    const list = document.querySelector("[data-private-link-list]");
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
            ? `${link.proposalCount} submission branch${link.proposalCount === 1 ? "" : "es"}`
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
  async function refreshLinks(id) {
    const data = await selectedWork(id);
    if (data) {
      renderLinks(data.links ?? []);
    }
  }
  function syncLinkButton(select) {
    const button = document.querySelector('button[data-share-action="link"]');
    if (!button) {
      return;
    }
    const option = select.selectedOptions[0];
    const published = option?.getAttribute("data-published") === "true";
    button.disabled = !published;
    button.title = published ? "" : "Publish this work first";
    const hint = document.querySelector(".verse-share-link-hint");
    if (hint) {
      hint.hidden = published;
    }
    const status = statusLine();
    if (status) {
      status.textContent = "";
    }
  }
  function init() {
    const select = picker();
    if (!select || select.dataset.shareWired === "true") {
      return;
    }
    select.dataset.shareWired = "true";
    select.addEventListener("change", () => {
      syncLinkButton(select);
      if (select.value) {
        refreshLinks(select.value);
      }
    });
    syncLinkButton(select);
    if (select.value) {
      refreshLinks(select.value);
    }
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
    for (const button of document.querySelectorAll(
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
          const data = await response.json();
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
          const ok = await copyText(`${data.title}

${data.body}`);
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
          const work = {
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
})();
