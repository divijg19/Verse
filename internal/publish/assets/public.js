(() => {
  // web/ts/public_search.ts
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
    const link = row.querySelector("a");
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
})();
