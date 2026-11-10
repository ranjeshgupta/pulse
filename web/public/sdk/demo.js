// Kept in a file (not inline) so the page works under a strict Content-Security-Policy.
var key = new URLSearchParams(location.search).get("key");
var who = document.getElementById("who");
function show() { who.textContent = "Events are recorded as: " + Pulse.currentId(); }
if (!key) { who.textContent = "Add ?key=YOUR_SITE_KEY to the address."; }
else {
  Pulse.init({ siteKey: key });
  show();
  document.getElementById("identify").onclick = function () { Pulse.identify(document.getElementById("uid").value.trim()); show(); };
  document.getElementById("buy").onclick = function () { Pulse.track("purchase", { amount: 499 }); Pulse.flush(); };
}
