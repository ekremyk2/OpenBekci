// Runs before first paint. Keep in sync with src/app/theme.ts (storage key and class name).
;(function () {
  try {
    var pref = localStorage.getItem('ob.theme')
    var dark =
      pref === 'dark' ||
      ((pref === null || pref === 'system') &&
        window.matchMedia('(prefers-color-scheme: dark)').matches)
    document.documentElement.classList.toggle('dark', dark)
  } catch (e) {
    /* storage unavailable: CSS falls back to the light theme */
  }
})()
