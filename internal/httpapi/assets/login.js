(function () {
  var form = document.getElementById('loginForm');
  var input = document.getElementById('password');
  var button = document.getElementById('submit');
  var error = document.getElementById('error');
  form.addEventListener('submit', async function (event) {
    event.preventDefault();
    error.textContent = '';
    button.disabled = true;
    button.textContent = '验证中…';
    try {
      var response = await fetch('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-WOL-Config': '1' },
        body: JSON.stringify({ password: input.value })
      });
      var payload = null;
      try { payload = await response.json(); } catch (e) {}
      if (!response.ok) {
        error.textContent = response.status === 429
          ? '登录失败次数过多，请稍后再试'
          : (payload && payload.error) || '访问密码错误';
        return;
      }
      window.location.reload();
    } catch (e) {
      error.textContent = '请求失败：' + (e.message || e);
    } finally {
      button.disabled = false;
      button.textContent = '继续';
    }
  });
})();
