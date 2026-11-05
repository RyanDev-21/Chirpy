// Fills in the download button from the API (so a release only needs the
// APP_* settings on the server, not a website change).
"use strict";
(async () => {
  const btn = document.getElementById("download");
  const label = document.getElementById("download-label");
  const info = document.getElementById("version");
  try {
    const res = await fetch("https://api.ponypigeon.com/api/app/version");
    const v = await res.json();
    if (!v.latest || !v.download_url || !v.download_url.startsWith("https://")) {
      info.textContent = "The first release is coming soon.";
      return;
    }
    btn.href = v.download_url;
    btn.removeAttribute("aria-disabled");
    label.textContent = "Download Chirpy " + v.latest + " for Android";
    info.textContent = "SHA-256: " + (v.sha256 || "not published");
    if (v.notes) document.getElementById("notes").textContent = v.notes;
  } catch {
    info.textContent = "Couldn't check the latest version right now. Try again in a minute.";
  }
})();
