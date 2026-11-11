// Runs before first paint. Keep in sync with src/app/theme.ts (storage keys, class name and data-look).
;(function () {
  try {
    var pref = localStorage.getItem('ob.theme')
    var dark =
      pref === 'dark' ||
      ((pref === null || pref === 'system') &&
        window.matchMedia('(prefers-color-scheme: dark)').matches)
    document.documentElement.classList.toggle('dark', dark)
    if (localStorage.getItem('ob.look') === 'tas') document.documentElement.dataset.look = 'tas'
  } catch (e) {
    /* storage unavailable: CSS falls back to the light theme */
  }
})()
