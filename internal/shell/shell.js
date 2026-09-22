/* furoshiki runtime
 *
 * The bundle is a single HTML file. Its payload (a JSON document embedded in
 * the <script type="application/json"> element below) contains every page of
 * the site and every resource the pages reference. This script is a hash
 * router: the fragment of the top-level URL names the page to display
 *
 *     bundle.html#/docs/guide.html?x=1#section
 *
 * and the page is shown in an <iframe srcdoc>. Before a page is displayed
 * every "furoshiki-res:<key>" placeholder in it is replaced with the data:
 * URL of the corresponding resource, so shared resources are stored once.
 *
 * A fresh iframe is created for every page so that loading it does not add
 * entries to the browser's session history; only the top-level fragment
 * navigations do, which makes Back and Forward behave as on the original
 * site.
 */
(function () {
  'use strict';

  var PLACEHOLDER = 'furoshiki-res:([0-9a-f]{16,64})';
  var VIRTUAL_ORIGIN = 'http://furoshiki.invalid';

  var view = document.getElementById('furoshiki-view');
  var payloadEl = document.getElementById('furoshiki-payload');

  function escapeHTML(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  function fatal(message) {
    if (!view) {
      return;
    }
    view.innerHTML = '<div class="furoshiki-message" role="alert"><h1>Cannot open this bundle</h1><p>' +
      escapeHTML(message) + '</p></div>';
  }

  var payload;
  try {
    payload = JSON.parse(payloadEl.textContent);
  } catch (err) {
    fatal('The embedded payload could not be parsed: ' + err);
    return;
  }
  if (!payload || payload.v !== 1 || !payload.pages) {
    fatal('The embedded payload has an unsupported format.');
    return;
  }

  var pages = payload.pages;
  var resources = payload.resources || {};
  var entry = payload.entry;

  var frame = null;        // the iframe currently displayed
  var current = null;      // the route currently displayed
  var currentID = null;    // history entry id of the displayed route
  var expanded = {};       // resource key -> data: URL, for text resources
  var scrollPositions = {}; // history entry id -> {x: number, y: number}

  /* ---------- resources ---------- */

  // expand replaces every placeholder in text with the data: URL of the
  // resource it names. Text resources (style sheets, scripts) may contain
  // placeholders themselves; they are expanded recursively, once, and the
  // result is cached. Unknown keys and cycles are left as they are.
  function expand(text, active) {
    return text.replace(new RegExp(PLACEHOLDER, 'g'), function (match, key) {
      var url = resourceURL(key, active);
      return url === null ? match : url;
    });
  }

  function resourceURL(key, active) {
    var r = resources[key];
    if (r === undefined) {
      return null;
    }
    if (typeof r === 'string') {
      return r;
    }
    if (Object.prototype.hasOwnProperty.call(expanded, key)) {
      return expanded[key];
    }
    if (active[key]) {
      return null;
    }
    active[key] = true;
    var url;
    try {
      url = 'data:' + r.mime + ';charset=utf-8,' + encodeURIComponent(expand(String(r.text), active));
    } catch (err) {
      url = null;
    }
    delete active[key];
    if (url !== null) {
      expanded[key] = url;
    }
    return url;
  }

  /* ---------- routes ---------- */

  function decodeSegment(s) {
    try {
      return decodeURIComponent(s);
    } catch (err) {
      return s;
    }
  }

  // parseRoute turns "#/a/b.html?q#frag" into {path, query, frag}. It
  // returns null for anything that is not a route.
  function parseRoute(hash) {
    if (typeof hash !== 'string' || hash.slice(0, 2) !== '#/') {
      return null;
    }
    var s = hash.slice(2);
    var frag = null;
    var i = s.indexOf('#');
    if (i >= 0) {
      frag = s.slice(i + 1);
      s = s.slice(0, i);
    }
    var query = '';
    i = s.indexOf('?');
    if (i >= 0) {
      query = s.slice(i + 1);
      s = s.slice(0, i);
    }
    return { path: s.split('/').map(decodeSegment).join('/'), query: query, frag: frag };
  }

  function buildHash(path, query, frag) {
    var h = '#/' + path.split('/').map(encodeURIComponent).join('/');
    if (query) {
      h += '?' + query;
    }
    if (frag !== null && frag !== undefined && frag !== '') {
      h += '#' + frag;
    }
    return h;
  }

  // lookupPage finds the page for a route path, treating a directory as its
  // index.html like the bundler does. It returns the page's key or null.
  function lookupPage(path) {
    if (Object.prototype.hasOwnProperty.call(pages, path)) {
      return path;
    }
    var dir = path === '' || path.slice(-1) === '/' ? path : path + '/';
    if (Object.prototype.hasOwnProperty.call(pages, dir + 'index.html')) {
      return dir + 'index.html';
    }
    return null;
  }

  function notFoundHTML(path) {
    return '<!DOCTYPE html><html><head><meta charset="utf-8"><title>Page not found</title>' +
      '<style>body{font:16px/1.6 system-ui,sans-serif;color:#222;max-width:40rem;margin:0 auto;padding:2rem 1.5rem}' +
      'code{background:#f3f3f3;padding:0 .3em;border-radius:3px}</style></head><body>' +
      '<h1>Page not found</h1><p>This bundle does not contain a page named <code>' + escapeHTML(path) + '</code>.</p>' +
      '<p><a href="' + escapeHTML(buildHash(entry, '', null)) + '" target="_top">Go to the start page</a></p>' +
      '</body></html>';
  }

  /* ---------- history / scroll restoration ---------- */

  // Every history entry is tagged with a random id (in history.state) the
  // first time it is displayed. When the user travels back to an entry the id
  // is already there, which tells us to restore the scroll position that was
  // saved when the entry was left.
  function entryID() {
    var s = history.state;
    return s && typeof s.furoshiki === 'string' ? s.furoshiki : null;
  }

  function tagEntry(url) {
    var id = entryID();
    if (id !== null) {
      return id;
    }
    id = Math.random().toString(36).slice(2) + Date.now().toString(36);
    try {
      history.replaceState({ furoshiki: id }, '', url || location.href);
    } catch (err) {
      return null;
    }
    return id;
  }

  // saveScroll remembers the scroll position of the route being left under
  // the id of its history entry. It runs on hashchange, when history.state
  // already belongs to the new entry, so the id is taken from currentID.
  function saveScroll() {
    if (currentID === null || !frame) {
      return;
    }
    try {
      var w = frame.contentWindow;
      scrollPositions[currentID] = { x: w.scrollX || w.pageXOffset || 0, y: w.scrollY || w.pageYOffset || 0 };
    } catch (err) {
      /* the frame is gone */
    }
  }

  function scrollToFragment(f, frag) {
    try {
      var doc = f.contentDocument;
      var win = f.contentWindow;
      if (!doc || !win) {
        return;
      }
      if (frag === null || frag === '') {
        win.scrollTo(0, 0);
        return;
      }
      var decoded = decodeSegment(frag);
      var el = doc.getElementById(decoded) || doc.getElementById(frag);
      if (!el) {
        var named = doc.getElementsByName(decoded);
        el = named.length ? named[0] : null;
      }
      if (el) {
        el.scrollIntoView();
      } else if (decoded.toLowerCase() === 'top') {
        win.scrollTo(0, 0);
      }
    } catch (err) {
      /* ignore */
    }
  }

  /* ---------- rendering ---------- */

  function show(route) {
    var key = lookupPage(route.path);
    var page = key === null ? null : pages[key];
    var sameDocument = current !== null && frame !== null &&
      current.path === route.path && current.query === route.query;

    saveScroll();
    var restoreID = entryID();
    var restore = restoreID !== null && scrollPositions[restoreID] ? scrollPositions[restoreID] : null;

    if (sameDocument) {
      // Only the fragment changed: scroll, do not reload. Travelling back
      // to an entry restores where the user was; a new entry scrolls to
      // its fragment.
      current = route;
      if (restore) {
        try {
          frame.contentWindow.scrollTo(restore.x, restore.y);
        } catch (err) {
          /* ignore */
        }
      } else {
        scrollToFragment(frame, route.frag);
      }
      currentID = tagEntry();
      return;
    }

    var next = document.createElement('iframe');
    next.setAttribute('id', 'furoshiki-frame');
    next.setAttribute('title', page ? (page.title || route.path) : 'Page not found');
    next.setAttribute('allowfullscreen', '');
    next.setAttribute('allow', 'fullscreen');
    next.addEventListener('load', function () {
      if (next !== frame) {
        return;
      }
      if (restore) {
        try {
          next.contentWindow.scrollTo(restore.x, restore.y);
        } catch (err) {
          /* ignore */
        }
      } else if (route.frag !== null && route.frag !== '') {
        scrollToFragment(next, route.frag);
      }
      currentID = tagEntry();
      try {
        next.contentWindow.focus();
      } catch (err) {
        /* ignore */
      }
    });
    next.srcdoc = page ? expand(page.html, {}) : notFoundHTML(route.path);

    if (frame) {
      view.replaceChild(next, frame);
    } else {
      view.appendChild(next);
    }
    frame = next;
    current = route;
    document.title = page ? (page.title || route.path) : 'Page not found';
  }

  function onHashChange() {
    var route = parseRoute(location.hash);
    if (route === null) {
      route = { path: entry, query: '', frag: null };
      try {
        history.replaceState(history.state, '', buildHash(entry, '', null));
      } catch (err) {
        /* keep the URL as it is */
      }
    }
    show(route);
  }

  /* ---------- API used by the shim inside pages ---------- */

  // navigate resolves a link found inside the page fromRel and, when it
  // points at a page of the bundle, navigates to it and returns true.
  // Otherwise it returns false and the caller lets the browser proceed.
  function navigate(raw, fromRel) {
    if (typeof raw !== 'string') {
      return false;
    }
    raw = raw.replace(/^[\s\u0000-\u001f]+|[\s\u0000-\u001f]+$/g, '').replace(/[\t\n\r]/g, '');
    if (raw === '') {
      return false;
    }
    if (raw.charAt(0) === '#') {
      if (raw.slice(0, 2) === '#/') {
        location.hash = raw;
        return true;
      }
      return false;
    }
    var u;
    try {
      u = new URL(raw, VIRTUAL_ORIGIN + '/' + (fromRel || entry));
    } catch (err) {
      return false;
    }
    if (u.origin !== VIRTUAL_ORIGIN) {
      return false;
    }
    var path = u.pathname.replace(/^\//, '').split('/').map(decodeSegment).join('/');
    var key = lookupPage(path);
    if (key === null) {
      return false;
    }
    location.hash = buildHash(key, u.search.slice(1), u.hash === '' ? null : u.hash.slice(1));
    return true;
  }

  function setTitle(title) {
    if (typeof title === 'string' && title !== '') {
      document.title = title;
    }
  }

  window.__furoshiki = {
    generator: payload.generator,
    entry: entry,
    pages: Object.keys(pages),
    navigate: navigate,
    setTitle: setTitle,
    parseRoute: parseRoute,
    buildHash: buildHash,
    expand: function (text) { return expand(String(text), {}); }
  };

  /* ---------- printing ---------- */

  // Ctrl/Cmd+P on the top document prints the embedded page instead, so the
  // whole page is printed rather than the part visible in the frame.
  document.addEventListener('keydown', function (e) {
    if ((e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && (e.key === 'p' || e.key === 'P')) {
      if (frame && frame.contentWindow) {
        e.preventDefault();
        try {
          frame.contentWindow.print();
        } catch (err) {
          /* ignore */
        }
      }
    }
  });

  // When the top document itself is printed (browser menu), the frame is
  // stretched to the full height of the page so nothing is cut off.
  window.addEventListener('beforeprint', function () {
    if (!frame) {
      return;
    }
    try {
      var doc = frame.contentDocument;
      var h = Math.max(doc.documentElement.scrollHeight, doc.body ? doc.body.scrollHeight : 0);
      if (h > 0) {
        frame.style.height = h + 'px';
      }
      document.documentElement.classList.add('furoshiki-print');
    } catch (err) {
      /* ignore */
    }
  });
  window.addEventListener('afterprint', function () {
    if (frame) {
      frame.style.height = '';
    }
    document.documentElement.classList.remove('furoshiki-print');
  });

  /* ---------- start ---------- */

  window.addEventListener('hashchange', onHashChange);
  onHashChange();
})();
