const tags = ['p', 'br', 'strong', 'em', 'del', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'ul', 'ol', 'li', 'blockquote', 'pre', 'code', 'hr', 'a', 'table', 'thead', 'tbody', 'tr', 'th', 'td'];

export function renderAnswer(element, text) {
  if (!globalThis.marked?.parse || !globalThis.DOMPurify?.sanitize) {
    element.textContent = text;
    element.classList.add('plain-text');
    return;
  }
  try {
    const fragment = DOMPurify.sanitize(marked.parse(text, {gfm: true, breaks: true}), {
      ALLOWED_TAGS: tags, ALLOWED_ATTR: ['href', 'title', 'start'],
      ALLOW_DATA_ATTR: false, ALLOW_ARIA_ATTR: false, RETURN_DOM_FRAGMENT: true,
    });
    for (const link of fragment.querySelectorAll('a')) {
      const href = link.getAttribute('href');
      // Only explicitly navigable web links survive; document paths remain readable text.
      if (!href || !/^https?:\/\//i.test(href)) link.removeAttribute('href');
      else {
        link.setAttribute('target', '_blank');
        link.setAttribute('rel', 'noopener noreferrer');
      }
    }
    element.replaceChildren(fragment);
  } catch {
    element.textContent = text;
    element.classList.add('plain-text');
  }
}
