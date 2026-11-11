// Pulse web push service worker. Serve a file at your site's root (e.g. /pulse-sw.js) containing:
//   importScripts("https://YOUR-PULSE/sdk/pulse-sw.js");
// It shows Pulse notifications, opens their link when clicked, and reports clicks and dismissals
// to Pulse (push_clicked / push_dismissed on the message, for reports and journey decisions).
self.addEventListener("push", (event) => {
  let d = {};
  try { d = event.data ? event.data.json() : {}; } catch (e) { d = { title: "", body: event.data ? event.data.text() : "" }; }
  const data = d.data || {};
  event.waitUntil(self.registration.showNotification(d.title || "", {
    body: d.body || "", image: d.image || undefined, icon: data.icon || undefined,
    data: { url: d.url || "", message_id: d.message_id || "", track: data.pulse_track || "" }, tag: d.message_id || undefined,
  }));
});

// report posts a click or dismissal to the message's tracking link (a sealed token: no cookies, no
// body, and the answer isn't needed, so the request needs no CORS preflight).
function report(notification, action) {
  const track = notification.data && notification.data.track;
  if (!track || !/^https?:\/\//.test(track)) return Promise.resolve();
  return fetch(track + "?a=" + action + "&platform=web", { method: "POST", mode: "no-cors", keepalive: true, credentials: "omit" })
    .catch(() => {});
}

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = event.notification.data && event.notification.data.url;
  const open = url
    ? self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((wins) => {
      for (const w of wins) if (w.url === url && "focus" in w) return w.focus();
      return self.clients.openWindow(url);
    })
    : Promise.resolve();
  event.waitUntil(Promise.all([open, report(event.notification, "clicked")]));
});

self.addEventListener("notificationclose", (event) => {
  event.waitUntil(report(event.notification, "dismissed"));
});
