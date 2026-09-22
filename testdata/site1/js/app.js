// Shared by several pages; must be stored once in the bundle.
(function () {
  'use strict';
  var el = document.getElementById('js-status');
  if (el) {
    el.textContent = 'js ran on ' + document.documentElement.getAttribute('data-furoshiki-page');
  }
})();
