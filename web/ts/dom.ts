/**
 * The closest matching ancestor of an event target, or null.
 *
 * The target of a click or an input event is not always an Element -- it can be a text node or the
 * document itself -- and calling closest on those is a TypeError rather than a false result. An
 * inline attribute never had to consider it, because the browser passed the element.
 *
 * It was written twice: once here and once inline in the navigation bundle. The two bundles are
 * separate files, so a helper both of them need is a module both of them import.
 */
export function closestElement(
  target: EventTarget | null,
  selector: string,
): Element | null {
  return target instanceof Element ? target.closest(selector) : null;
}
