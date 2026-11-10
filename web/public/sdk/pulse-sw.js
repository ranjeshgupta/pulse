// Pulse web push service worker. Serve a file at your site's root (e.g. /pulse-sw.js) containing:
//   importScripts("https://YOUR-PULSE/sdk/pulse-sw.js");
// It shows Pulse notifications and opens their link when clicked.
self.addEventListener("push", (event) => {
  let d = {};
  try { d = event.data ? event.data.json() : {}; } catch (e) { d = { title: "", body: event.data ? event.data.text() : "" }; }
  event.waitUntil(self.registration.showNotification(d.title || "", {
    body: d.body || "", image: d.image || undefined, icon: (d.data && d.data.icon) || undefined,
    data: { url: d.url || "", message_id: d.message_id || "" }, tag: d.message_id || undefined,
  }));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = event.notification.data && event.notification.data.url;
  if (!url) return;
  event.waitUntil(self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((wins) => {
    for (const w of wins) if (w.url === url && "focus" in w) return w.focus();
    return self.clients.openWindow(url);
  }));
});
