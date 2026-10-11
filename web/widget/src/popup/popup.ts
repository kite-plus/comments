// The login popup's pages (login.md section 4.2). "begin": say ready to the
// widget that opened the popup, take {site, challenge} only from it at the
// service's own origin, create the transaction and go to the identity
// service. "done": tell the widget and close; the widget's polling finishes
// the login even when this message is lost.
const KC = 1;
const serviceOrigin = document.body.dataset.origin ?? "";
const step = document.body.dataset.step;
const show = (id: string) => document.getElementById(id)?.removeAttribute("hidden");

if (step === "begin") {
  const opener = window.opener as Window | null;
  if (!opener) {
    show("kc-no-opener");
  } else {
    let begun = false;
    addEventListener("message", async (e: MessageEvent) => {
      const d = e.data;
      if (begun || e.source !== opener || e.origin !== serviceOrigin || !d || d.kc !== KC || d.type !== "begin") return;
      if (typeof d.site !== "string" || typeof d.challenge !== "string") return;
      begun = true;
      try {
        const r = await fetch("/auth/transactions", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ site: d.site, challenge: d.challenge }),
        });
        if (!r.ok) throw new Error(String(r.status));
        const { authorize_url } = (await r.json()) as { authorize_url: string };
        location.assign(authorize_url);
      } catch {
        show("kc-failed");
      }
    });
    // Say ready a few times, in case the widget was busy the first time.
    let tries = 0;
    const ready = () => {
      if (begun || tries++ > 6) return;
      opener.postMessage({ kc: KC, type: "ready" }, serviceOrigin);
      setTimeout(ready, 500);
    };
    ready();
  }
} else if (step === "done") {
  const opener = window.opener as Window | null;
  try {
    opener?.postMessage({ kc: KC, type: "done" }, serviceOrigin);
  } catch {
    // The opener may be gone; polling still completes the login.
  }
  setTimeout(() => window.close(), 300);
}
export {};
