document.getElementById('loginForm').addEventListener('submit', function(e) {
  e.preventDefault();
  var token = document.getElementById('tokenInput').value.trim();
  if (!token) {
    document.getElementById('errorMsg').textContent = 'Введите токен';
    document.getElementById('errorMsg').style.display = 'block';
    return;
  }
  localStorage.setItem('vaultdb_token', token);
  fetch('/health', { headers: { 'Authorization': 'Bearer ' + token } })
    .then(function(r) { return r.json(); })
    .then(function(h) {
      if (h.status === 'ok') {
        window.location.href = '/';
      } else {
        document.getElementById('errorMsg').textContent = 'Неверный токен';
        document.getElementById('errorMsg').style.display = 'block';
      }
    })
    .catch(function() {
      document.getElementById('errorMsg').textContent = 'Ошибка подключения';
      document.getElementById('errorMsg').style.display = 'block';
    });
});

// Auto-fill from localStorage
var saved = localStorage.getItem('vaultdb_token');
if (saved) document.getElementById('tokenInput').value = saved;
