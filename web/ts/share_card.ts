// The shared logic behind the three Share actions.
//
// Copy Text, Copy Link and Card Export appear on both surfaces: the reader's static site and the
// author's private application. The two surfaces have different bundles, so without this module the
// three actions would be written twice and could drift apart -- and a copy button that copies the
// wrong thing is worse than one that does nothing, because it says it succeeded.
//
// Nothing here makes a request to a third party. The card is drawn on a canvas from text the page
// already has, and the clipboard is a browser API. That is what keeps the public site's rule -- one
// script, no inline script, no foreign request -- intact while still offering real actions.

/** The text a card is drawn from. */
export interface ShareWork {
  /** The work's title, which is its first line verbatim. */
  title: string;

  /** The work's body, without its title. */
  body: string;

  /** The absolute address of the work on the public site. */
  address: string;

  /** The publication date as an ISO day, or empty for a draft. */
  published: string;
}

/**
 * Writes text to the clipboard, and reports whether it succeeded.
 *
 * The Clipboard API needs a secure context and a user gesture; both are true when this is called
 * from a click handler on the deployed HTTPS site. It is also unavailable in some embedded
 * browsers and when the reader has denied permission, so a failure is a normal outcome rather than
 * an exception. The callers use the result to say what happened instead of guessing.
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Fall through to the selection fallback below.
  }

  // A hidden textarea is the fallback for browsers without the Clipboard API. It is placed inside
  // the viewport because some browsers refuse to select an off-screen element, and removed again
  // whether the copy worked or not.
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

/** The font stack the site already uses, so a card looks like the page it came from. */
const titleFont = 'italic 44px Georgia, Cambria, "Times New Roman", serif';
const bodyFont = '28px Georgia, Cambria, "Times New Roman", serif';
const metaFont = "18px ui-sans-serif, system-ui, sans-serif";

/**
 * Wraps a paragraph at the given width, using the given canvas context's current font.
 *
 * Measured rather than estimated: a character count would be wrong for a proportional serif, and a
 * broken line in an exported card is obvious. Words longer than the width are broken on characters,
 * so a long word can never overflow the canvas.
 */
function wrap(
  ctx: CanvasRenderingContext2D,
  text: string,
  maxWidth: number,
): string[] {
  const lines: string[] = [];

  for (const paragraph of text.split("\n")) {
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

      // A single word wider than the line: break it on characters.
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

/** Draws a work into a canvas and returns it. */
export function renderCard(work: ShareWork): HTMLCanvasElement {
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
  // The card is a fixed size, so a very long work is cut where it runs out of room. The exported
  // text is never cut -- only the picture is -- and the address below points at the whole work.
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

/**
 * Draws a work and starts a PNG download.
 *
 * toBlob is asynchronous and may return null if the browser cannot encode the image, so the caller
 * is told through a returned promise. Nothing is written to the network.
 */
export function savePNG(work: ShareWork): Promise<boolean> {
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

      // Revoking immediately can cancel the download in some browsers; the next task is soon
      // enough for the browser to have taken the blob, and still frees it without a timer.
      queueMicrotask(() => URL.revokeObjectURL(url));
      resolve(true);
    }, "image/png");
  });
}
