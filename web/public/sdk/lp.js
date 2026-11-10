// Landing page bootstrap for Pulse-hosted pages (/p/...). Kept in a file, not inline, so pages work
// under their strict Content-Security-Policy. Starts the SDK with the workspace's site key (page
// views, embedded forms) and records landing_page_viewed with the page id and any utm_* parameters.
// The SDK itself keeps the visitor's first- and last-touch campaign (first_utm_*, last_utm_*), sent
// with form submissions and identify, and puts the session's campaign on page views.
(function () {
  var s = document.currentScript;
  var key = s && s.getAttribute("data-site-key");
  var page = Number(s && s.getAttribute("data-page-id"));
  if (!key || !window.Pulse) return;
  // Pages of every workspace share this origin: keep each workspace's visitor ids apart.
  Pulse.init({ siteKey: key, storagePrefix: key });
  var props = { page_id: page, url: location.href, referrer: document.referrer };
  try {
    var ref = document.referrer && new URL(document.referrer).hostname;
    if (ref && ref !== location.hostname) props.referrer_domain = ref;
  } catch (e) { /* no referrer */ }
  new URLSearchParams(location.search).forEach(function (v, k) {
    if (/^utm_[a-z_]{1,30}$/.test(k)) props[k] = v.slice(0, 200);
  });
  Pulse.track("landing_page_viewed", props);
  Pulse.flush();
})();
