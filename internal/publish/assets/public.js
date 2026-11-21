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

  // web/ts/public.ts
  var indexURL = "/search.json";
  var list = document.querySelector(".work-list");
  async function readIndex() {
    try {
      const response = await fetch(indexURL, {
        headers: { accept: "application/json" },
      });
      if (!response.ok) {
        return null;
      }
      const body = await response.json();
      if (typeof body !== "object" || body === null) {
        return null;
      }
      const index = body;
      if (!Array.isArray(index.entries)) {
        return null;
      }
      return index;
    } catch {
      return null;
    }
  }
  function entryFor(row, entries) {
    const link = row.querySelector("a.share-title");
    const path = link ? link.getAttribute("href") : null;
    return path ? (entries.get(path) ?? null) : null;
  }
  function rowText(row, entries) {
    const entry = entryFor(row, entries);
    const title = row.querySelector(".work-title")?.textContent ?? "";
    return `${title} ${entry?.excerpt ?? ""}`.toLowerCase();
  }
  function highlight(row, entry, needle) {
    const element = row.querySelector(".work-excerpt");
    if (!element || !entry) {
      return;
    }
    const plain = entry.excerpt;
    if (needle === "") {
      element.replaceChildren(document.createTextNode(plain));
      return;
    }
    const haystack = plain.toLowerCase();
    const parts = [];
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
      element.replaceChildren(document.createTextNode(plain));
      return;
    }
    if (rest.length > 0) {
      parts.push(document.createTextNode(rest));
    }
    element.replaceChildren(...parts);
  }
  function applyFilter(rows, needle, entries) {
    let shown = 0;
    for (const row of rows) {
      const matches = needle === "" || rowText(row, entries).includes(needle);
      row.hidden = !matches;
      highlight(row, entryFor(row, entries), needle);
      if (matches) {
        shown += 1;
      }
    }
    return shown;
  }
  async function main() {
    if (!list) {
      return;
    }
    const rows = Array.from(list.querySelectorAll("li"));
    if (rows.length === 0) {
      return;
    }
    const index = await readIndex();
    if (!index) {
      return;
    }
    const entries = new Map();
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
    const update = () => {
      const needle = box.value.trim().toLowerCase();
      const shown = applyFilter(rows, needle, entries);
      if (needle === "") {
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
  main();
  async function loadWork(path, title, address, published) {
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
  function report(button, message) {
    const card = button.closest(".share-item");
    const status = card?.querySelector(".share-status");
    if (status) {
      status.textContent = message;
    }
  }
  function wireCard(card) {
    const title = card.querySelector(".share-title")?.textContent?.trim() ?? "";
    const link = card.querySelector("a.share-title");
    const path = link?.getAttribute("href") ?? "";
    const address = card.dataset.shareAddress ?? "";
    const published =
      card.querySelector("time")?.getAttribute("datetime") ?? "";
    const actions = card.querySelectorAll("button[data-share-action]");
    for (const button of actions) {
      button.addEventListener("click", async () => {
        const action = button.getAttribute("data-share-action");
        if (action === "link") {
          const ok = await copyText(address);
          report(
            button,
            ok ? "Address copied." : "Could not copy the address.",
          );
          return;
        }
        const work = await loadWork(path, title, address, published);
        if (!work) {
          report(button, "The work could not be loaded.");
          return;
        }
        if (action === "text") {
          const ok = await copyText(`${work.title}

${work.body}`);
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
    const actionsBox = card.querySelector(".share-actions");
    if (actionsBox) {
      actionsBox.hidden = false;
    }
  }
  function wireArchiveActions() {
    for (const card of document.querySelectorAll(".share-item")) {
      wireCard(card);
    }
  }
  wireArchiveActions();
})();
