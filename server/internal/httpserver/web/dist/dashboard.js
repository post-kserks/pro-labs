async function refresh() {
  const resp = await fetch('/metrics');
  const text = await resp.text();
  const lines = text.split('\n');
  const container = document.getElementById('metrics');
  container.textContent = '';
  for (const line of lines) {
    if (line.startsWith('#') || line.trim() === '') continue;
    const parts = line.split(' ');
    if (parts.length === 2) {
      const div = document.createElement('div');
      div.className = 'metric';
      const h3 = document.createElement('h3');
      h3.textContent = parts[0];
      const val = document.createElement('div');
      val.className = 'value';
      val.textContent = parts[1];
      div.appendChild(h3);
      div.appendChild(val);
      container.appendChild(div);
    }
  }
}
refresh();
setInterval(refresh, 5000);
