// Guardian's service worker: shows pushes and opens Guardian when tapped.
self.addEventListener("push", function (event) {
  var m = {};
  try { m = event.data ? event.data.json() : {}; } catch (e) {}
  event.waitUntil(self.registration.showNotification(m.title || "Guardian", {
    body: m.body || "",
    data: { url: m.url || "/" },
    icon: "/icon-192.png",
    badge: "/icon-192.png"
  }));
});
self.addEventListener("notificationclick", function (event) {
  event.notification.close();
  var url = (event.notification.data && event.notification.data.url) || "/";
  event.waitUntil(self.clients.openWindow(url));
});
