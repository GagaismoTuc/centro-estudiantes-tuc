// Applies the stored Claro/Oscuro override before first paint (no flash of the wrong theme).
// A classic, blocking script on purpose; kept external so a strict CSP needs no inline hash.
(function () {
  var theme = null;
  try {
    theme = localStorage.getItem("color-scheme");
  } catch (e) {}
  if (theme !== "light" && theme !== "dark") theme = null;
  document.documentElement.setAttribute("data-theme", theme || "system");
  var meta = document.querySelector('meta[name="color-scheme"]');
  if (meta && theme) meta.content = theme;
})();
