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

  // web/ts/scroll_lock.ts
  function bodyScrollLockCount() {
    return Number(document.body.dataset.verseScrollLocks || "0");
  }
  function lockBodyScroll() {
    document.body.dataset.verseScrollLocks = String(bodyScrollLockCount() + 1);
    document.body.style.overflow = "hidden";
  }
  function unlockBodyScroll() {
    const next = Math.max(0, bodyScrollLockCount() - 1);
    if (next === 0) {
      delete document.body.dataset.verseScrollLocks;
      document.body.style.overflow = "";
      return;
    }
    document.body.dataset.verseScrollLocks = String(next);
  }

  // web/ts/share.ts
  var endedStates = ["expired", "revoked"];
  function isEnded(state) {
    return endedStates.includes(state);
  }
  function stateLabel(state) {
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
  function rowControls(state, kind) {
    const ended = isEnded(state);
    return {
      copy: state === "active",
      fresh: kind === "private" && !ended,
      rotate: kind === "public" && state !== "unpublished",
      revoke: !ended && state !== "unpublished",
    };
  }
  var suggestionsOpener = null;
  var suggestionsLocked = false;
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
  async function fetchSubmissions(id) {
    try {
      const response = await fetch(
        `/share/work/${encodeURIComponent(id)}/submissions`,
        { headers: { accept: "application/json" } },
      );
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
  function linkButtons(
    id,
    copyAttr,
    revokeAttr,
    controls,
    url,
    newAttr = null,
    rotateAttr = null,
  ) {
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
  function suggestionsDialog() {
    return document.querySelector("[data-suggestions-overlay]");
  }
  function closeSuggestions() {
    const dialog = suggestionsDialog();
    if (dialog?.open) {
      dialog.close();
    }
  }
  function renderSuggestions(submissions, total, unavailable) {
    const list = document.querySelector("[data-suggestions-list]");
    const status = document.querySelector("[data-suggestions-status]");
    if (!list || !status) {
      return;
    }
    list.replaceChildren();
    if (submissions.length === 0) {
      const empty = document.createElement("p");
      empty.className = "verse-suggestions-empty";
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
    if (unavailable) {
      status.textContent =
        "Suggestions are temporarily unavailable; showing what could be read.";
    } else if (total > submissions.length) {
      status.textContent = `Showing the ${submissions.length} most recent of ${total} suggestions.`;
    } else {
      status.textContent = "";
    }
  }
  function wireSuggestions() {
    const dialog = suggestionsDialog();
    if (!dialog || dialog.dataset.suggestionsWired === "true") {
      return;
    }
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
  async function openSuggestions(opener) {
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
      current.querySelector("[data-suggestions-close]")?.focus();
    });
  }
  function renderLinks(links) {
    const section = document.querySelector("[data-links]");
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
  function renderPublicLinks(links) {
    const section = document.querySelector("[data-links]");
    const list = document.querySelector("[data-public-link-list]");
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
  async function refreshLinks(id) {
    closeSuggestions();
    const data = await selectedWork(id);
    if (!data) {
      for (const selector of [
        "[data-private-link-list]",
        "[data-public-link-list]",
      ]) {
        const list = document.querySelector(selector);
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
  function syncLinkButton(select) {
    const option = select.selectedOptions[0];
    const published = option?.getAttribute("data-published") === "true";
    const configured =
      document
        .querySelector("[data-public-address-configured]")
        ?.getAttribute("data-public-address-configured") === "true";
    for (const button of document.querySelectorAll(
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
    const hint = document.querySelector(".verse-share-link-hint");
    if (hint) {
      hint.hidden = published && configured;
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
    wireSuggestions();
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
        if (target.dataset.suggestionsClose !== undefined) {
          closeSuggestions();
          return;
        }
        if (target.dataset.suggestionsOpen !== undefined) {
          openSuggestions(target);
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
          const data = await response.json();
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
          const data = await response.json();
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
            copyText(url).then((ok) =>
              report(
                ok
                  ? "Link copied."
                  : "Could not copy this browser's clipboard.",
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
          const data = await response.json();
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
          const data = await response.json();
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
  document.body.addEventListener("htmx:afterSwap", () => {
    if (
      suggestionsLocked &&
      !document.querySelector("[data-suggestions-overlay]")
    ) {
      unlockBodyScroll();
      suggestionsLocked = false;
    }
  });
})();
