// Runs before first paint so a saved theme or language never flashes. It is a
// file rather than an inline script so the page works under a strict CSP.
;(function () {
  try {
    var theme = localStorage.getItem('theme')
    if (theme === 'light' || theme === 'dark') document.documentElement.dataset.theme = theme
    var lang = localStorage.getItem('kc-lang')
    if (lang === 'zh-CN' || lang === 'en') document.documentElement.lang = lang
  } catch (e) {
    // Storage blocked: follow the system.
  }
})()
