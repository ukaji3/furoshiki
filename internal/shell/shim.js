/* furoshiki page shim
 *
 * Injected at the start of <head> of every bundled page. The page runs inside
 * an <iframe srcdoc> of the bundle's shell document; this shim connects the
 * two:
 *
 *   - clicks on links that lead to other pages of the site (including links
 *     created by the page's own scripts, which the bundler could not rewrite)
 *     navigate the shell instead of the frame;
 *   - Ctrl/Cmd+P prints this page rather than the shell around it;
 *   - changes to document.title are mirrored to the shell.
 *
 * Everything is best-effort: when the shell cannot be reached the shim does
 * nothing and the browser's default behaviour applies.
 */
(function () {
  'use strict';

  var XLINK = 'http://www.w3.org/1999/xlink';
  var pageRel = document.documentElement.getAttribute('data-furoshiki-page') || '';

  // host finds the shell's API in an ancestor window.
  function host() {
    var w = window;
    try {
      while (w.parent && w.parent !== w) {
        w = w.parent;
        if (w.__furoshiki && typeof w.__furoshiki.navigate === 'function') {
          return w.__furoshiki;
        }
      }
    } catch (err) {
      /* cross-origin ancestor; not a bundle shell */
    }
    return null;
  }

  function anchorOf(node) {
    while (node && node.nodeType === 1) {
      var tag = String(node.tagName).toLowerCase();
      if (tag === 'a' || tag === 'area') {
        return node;
      }
      node = node.parentNode;
    }
    return null;
  }

  function hrefOf(a) {
    var raw = a.getAttribute('href');
    if (raw === null && a.namespaceURI === 'http://www.w3.org/2000/svg') {
      raw = a.getAttributeNS(XLINK, 'href');
    }
    return raw;
  }

  document.addEventListener('click', function (e) {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) {
      return;
    }
    var a = anchorOf(e.target);
    if (a === null || a.hasAttribute('download')) {
      return;
    }
    var target = String(a.getAttribute('target') || '').toLowerCase();
    if (target !== '' && target !== '_self' && target !== '_top' && target !== '_parent') {
      return;
    }
    var raw = hrefOf(a);
    if (raw === null || raw === '') {
      return;
    }
    var shell = host();
    if (shell === null) {
      return;
    }
    var handled = false;
    try {
      handled = shell.navigate(raw, pageRel) === true;
    } catch (err) {
      handled = false;
    }
    if (handled) {
      e.preventDefault();
    }
  }, false);

  document.addEventListener('keydown', function (e) {
    if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && (e.key === 'p' || e.key === 'P')) {
      e.preventDefault();
      window.print();
    }
  }, false);

  if (typeof MutationObserver === 'function') {
    var lastTitle = document.title;
    var observer = new MutationObserver(function () {
      if (document.title === lastTitle) {
        return;
      }
      lastTitle = document.title;
      var shell = host();
      if (shell !== null) {
        try {
          shell.setTitle(lastTitle);
        } catch (err) {
          /* ignore */
        }
      }
    });
    // The shim runs before <title> exists, so observe the whole head.
    observer.observe(document.head || document.documentElement, { subtree: true, childList: true, characterData: true });
  }
})();
