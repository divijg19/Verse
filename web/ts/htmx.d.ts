// Ambient declarations for htmx.
//
// htmx 2.0.4 ships no types of its own, and the vendored file is a dependency's bytes rather than
// this project's, so it is neither type-checked nor re-exported. What is declared here is the four
// events this application listens for and the one field of their detail it reads.
//
// The detail shape is htmx's own: `target` is the element the request is for. It is the field the
// screen-transition code compares against the #screen element to decide whether a swap is one of
// ours or a swap somewhere else on the page.
//
// The events are declared on both event maps because the listeners are attached to document.body,
// which resolves its event names through the HTMLElement map, and to the document itself, which
// resolves them through the document map.

export {};

declare global {
  interface VerseHtmxEventDetail {
    // target is the element the request was made for.
    target: Element;

    // elt is the element that triggered it.
    elt?: Element;
  }

  interface DocumentEventMap {
    "htmx:beforeRequest": CustomEvent<VerseHtmxEventDetail>;
    "htmx:afterSwap": CustomEvent<VerseHtmxEventDetail>;
    "htmx:afterRequest": CustomEvent<VerseHtmxEventDetail>;
    "htmx:responseError": CustomEvent<VerseHtmxEventDetail>;
  }

  interface HTMLElementEventMap {
    "htmx:beforeRequest": CustomEvent<VerseHtmxEventDetail>;
    "htmx:afterSwap": CustomEvent<VerseHtmxEventDetail>;
    "htmx:afterRequest": CustomEvent<VerseHtmxEventDetail>;
    "htmx:responseError": CustomEvent<VerseHtmxEventDetail>;
  }
}
