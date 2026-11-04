(function () {
  try {
    var theme = localStorage.getItem("wolTheme") || "auto";
    if (theme !== "auto" && theme !== "light" && theme !== "dark") theme = "auto";
    document.documentElement.setAttribute("data-theme", theme);
    var meta = document.querySelector('meta[name="theme-color"]');
    if (meta) {
      var dark = theme === "dark" || (theme === "auto" && window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);
      meta.setAttribute("content", dark ? "#0b1220" : "#f3f6fb");
    }
  } catch (error) {
    document.documentElement.setAttribute("data-theme", "auto");
  }
})();
