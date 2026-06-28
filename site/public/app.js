(() => {
  const $ = (s, p = document) => p.querySelector(s);
  const $$ = (s, p = document) => [...p.querySelectorAll(s)];

  // --- State ---
  let currentDb = '';
  let features = [];

  // --- Navigation ---
  $$('.nav-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      $$('.nav-btn').forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      $$('.panel').forEach(p => p.classList.remove('active'));
      $(`#panel-${btn.dataset.panel}`).classList.add('active');
      if (btn.dataset.panel === 'schema') loadSchema();
      if (btn.dataset.panel === 'dashboard') loadDashboard();
    });
  });

  // --- Query execution ---
  async function runQuery(sql, database) {
    const db = database || currentDb;
    const resp = await fetch('/api/query', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ database: db, query: sql }),
    });
    return resp.json();
  }

  function renderResult(containerId, data) {
    const area = $(`#${containerId}`);
    const meta = $(`#${containerId}Meta`) || $(`#${containerId.replace('Result','')}Meta`);
    const table = $(`#${containerId.replace('Result','')}Table`) || $(`#${containerId}Table`);
    const msg = $(`#${containerId}Message`) || $(`#${containerId.replace('Result','')}Message`);

    if (!area) return;
    area.style.display = 'block';

    if (data.status === 'error') {
      if (meta) meta.textContent = 'Error';
      if (table) table.innerHTML = '';
      if (msg) { msg.textContent = data.message || 'Unknown error'; msg.style.display = 'block'; }
      return;
    }

    if (msg) msg.style.display = 'none';

    if (data.duration_ms !== undefined) {
      if (meta) meta.textContent = `${data.type || 'ok'} — ${data.affected || 0} affected`;
      const dur = $(`#${containerId.replace('Result','')}Duration`);
      if (dur) dur.textContent = `${data.duration_ms.toFixed(1)} ms`;
    } else {
      if (meta) meta.textContent = data.type || 'ok';
    }

    if (data.columns && data.rows) {
      let html = '<thead><tr>';
      data.columns.forEach(c => { html += `<th>${esc(c)}</th>`; });
      html += '</tr></thead><tbody>';
      data.rows.forEach(row => {
        html += '<tr>';
        row.forEach(cell => { html += `<td>${esc(String(cell))}</td>`; });
        html += '</tr>';
      });
      html += '</tbody>';
      if (table) table.innerHTML = html;
    } else if (data.message) {
      if (table) table.innerHTML = '';
      if (msg) { msg.textContent = data.message; msg.style.display = 'block'; }
    }
  }

  function esc(s) {
    const d = document.createElement('div');
    d.textContent = s;
    return d.innerHTML;
  }

  // --- SQL Playground ---
  const editor = $('#sqlEditor');
  const runBtn = $('#runBtn');
  const clearBtn = $('#clearBtn');

  async function executeEditor() {
    const sql = editor.value.trim();
    if (!sql) return;
    runBtn.disabled = true;
    runBtn.textContent = '⏳ Running...';
    try {
      const data = await runQuery(sql);
      renderResult('resultArea', data);
      if (data.columns && data.rows && currentDb === '' && sql.toUpperCase().includes('CREATE DATABASE')) {
        loadDatabases();
      }
    } catch (e) {
      renderResult('resultArea', { status: 'error', message: e.message });
    }
    runBtn.disabled = false;
    runBtn.textContent = '▶ Run Query';
  }

  runBtn.addEventListener('click', executeEditor);
  clearBtn.addEventListener('click', () => { editor.value = ''; $('#resultArea').style.display = 'none'; });
  editor.addEventListener('keydown', e => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') { e.preventDefault(); executeEditor(); }
  });

  // Chips
  $$('.chip[data-sql]').forEach(chip => {
    chip.addEventListener('click', async () => {
      const sql = chip.dataset.sql;
      const upper = sql.toUpperCase().trim();
      if (upper.startsWith('USE ')) {
        currentDb = sql.split(/\s+/)[1].replace(/;/g, '');
        $('#dbSelect').value = currentDb;
        editor.value = sql;
        return;
      }
      editor.value = sql;
      await executeEditor();
    });
  });

  // --- Database selector ---
  async function loadDatabases() {
    try {
      const resp = await fetch('/api/databases');
      const data = await resp.json();
      const sel = $('#dbSelect');
      const current = sel.value;
      sel.innerHTML = '<option value="">— select —</option>';
      (data.databases || []).forEach(db => {
        const opt = document.createElement('option');
        opt.value = db.name;
        opt.textContent = db.name;
        sel.appendChild(opt);
      });
      if (current) sel.value = current;
    } catch {}
  }

  $('#dbSelect').addEventListener('change', e => { currentDb = e.target.value; });

  // --- Schema Explorer ---
  async function loadSchema() {
    const tree = $('#schemaTree');
    tree.innerHTML = '<div class="tree-loading">Loading...</div>';
    try {
      const resp = await fetch('/api/databases');
      const data = await resp.json();
      const dbs = data.databases || [];
      if (dbs.length === 0) {
        tree.innerHTML = '<div class="tree-loading">No databases found.</div>';
        return;
      }
      let html = '';
      for (const db of dbs) {
        html += `<div class="tree-db" data-db="${esc(db.name)}">📁 ${esc(db.name)}</div>`;
        const tResp = await fetch(`/api/databases/${db.name}/tables`);
        const tData = await tResp.json();
        (tData.tables || []).forEach(t => {
          html += `<div class="tree-table" data-db="${esc(db.name)}" data-table="${esc(t.name)}">📄 ${esc(t.name)} <span style="color:var(--fg3)">(${t.row_count})</span></div>`;
        });
      }
      tree.innerHTML = html;

      $$('.tree-table', tree).forEach(el => {
        el.addEventListener('click', () => loadTableSchema(el.dataset.db, el.dataset.table));
      });
    } catch (e) {
      tree.innerHTML = `<div class="tree-loading">Error: ${esc(e.message)}</div>`;
    }
  }

  async function loadTableSchema(db, table) {
    const detail = $('#schemaDetail');
    detail.innerHTML = '<div class="loading">Loading...</div>';
    try {
      const resp = await fetch(`/api/databases/${db}/tables/${table}/schema`);
      const s = await resp.json();
      let html = `<h3>${esc(s.database)}.${esc(s.name)}</h3>`;
      html += `<p style="color:var(--fg2);font-size:13px;margin:8px 0">Rows: ${s.row_count || 0}</p>`;
      html += '<table><thead><tr><th>Column</th><th>Type</th><th>Constraints</th></tr></thead><tbody>';
      (s.columns || []).forEach(c => {
        const constraints = [];
        if (c.primary_key) constraints.push('PK');
        if (c.not_null) constraints.push('NOT NULL');
        html += `<tr><td>${esc(c.name)}</td><td>${esc(c.type)}</td><td style="color:var(--accent)">${constraints.join(', ')}</td></tr>`;
      });
      html += '</tbody></table>';
      detail.innerHTML = html;
    } catch (e) {
      detail.innerHTML = `<p style="color:var(--err)">Error: ${esc(e.message)}</p>`;
    }
  }

  // --- Transaction Lab ---
  const runTxBtn = $('#runTxBtn');
  runTxBtn.addEventListener('click', async () => {
    const steps = $$('.tx-code');
    const timeline = $('#txTimeline');
    const resultArea = $('#txResult');
    resultArea.style.display = 'none';
    timeline.innerHTML = '';
    let txDb = '';

    for (let i = 0; i < steps.length; i++) {
      const lines = steps[i].value.split('\n').filter(l => l.trim() && !l.trim().startsWith('--'));
      for (const line of lines) {
        const trimmed = line.trim();
        const upper = trimmed.toUpperCase();

        // Track USE statements
        if (upper.startsWith('USE ')) {
          txDb = trimmed.split(/\s+/)[1].replace(/;/g, '');
        }

        const entry = document.createElement('div');
        entry.className = 'tx-entry';
        entry.textContent = `> ${trimmed}`;
        timeline.appendChild(entry);

        const data = await runQuery(trimmed, txDb);
        if (data.status === 'error') {
          entry.classList.add('err');
          entry.textContent += ` — ERROR: ${data.message}`;
        } else {
          entry.classList.add('ok');
          if (data.message) entry.textContent += ` — ${data.message}`;
          if (data.columns && data.rows) {
            renderResult('txResult', data);
          }
        }
        timeline.scrollTop = timeline.scrollHeight;
      }
    }
  });

  // --- Time Travel ---
  const ttSetupBtn = $('#ttSetupBtn');
  ttSetupBtn.addEventListener('click', async () => {
    const lines = $('#ttSetup').value.split('\n').filter(l => l.trim());
    let db = '';
    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith('--')) continue;
      const upper = trimmed.toUpperCase();
      if (upper.startsWith('USE ')) {
        db = trimmed.split(/\s+/)[1].replace(/;$/, '');
        currentDb = db;
        continue;
      }
      if (upper.startsWith('CREATE DATABASE') || upper.startsWith('DROP DATABASE')) {
        await runQuery(trimmed);
        continue;
      }
      const data = await runQuery(trimmed, db);
      if (data.status === 'error') {
        const msg = (data.message || '').toLowerCase();
        if (msg.includes('does not exist') || msg.includes('already exists')) continue;
        renderResult('ttResult', { status: 'error', message: `${trimmed}: ${data.message}` });
        return;
      }
    }
    renderResult('ttResult', { status: 'ok', message: 'Setup complete. Try the time travel queries below.' });
  });

  $$('#panel-timetravel .chip').forEach(chip => {
    chip.addEventListener('click', async () => {
      const stmts = chip.dataset.sql.split(';').filter(s => s.trim());
      let lastResult;
      let db = currentDb;
      for (const stmt of stmts) {
        let trimmed = stmt.trim();
        const upper = trimmed.toUpperCase();
        if (upper.startsWith('USE ')) {
          db = trimmed.split(/\s+/)[1].replace(/;$/, '');
          currentDb = db;
          lastResult = { status: 'ok', message: `Using database '${db}'` };
        } else {
          if (!trimmed.endsWith(';')) trimmed += ';';
          lastResult = await runQuery(trimmed, db);
        }
      }
      renderResult('ttResult', lastResult);
    });
  });

  // --- Feature Gallery ---
  features = [
    {
      title: 'JOIN',
      desc: 'Combine rows from two tables based on a related column.',
      sql: `DROP DATABASE f_join; CREATE DATABASE f_join; USE f_join;
CREATE TABLE employees (id INT PRIMARY KEY, name TEXT, dept_id INT);
CREATE TABLE departments (id INT PRIMARY KEY, dept_name TEXT);
INSERT INTO employees VALUES (1, 'Alice', 1);
INSERT INTO employees VALUES (2, 'Bob', 1);
INSERT INTO employees VALUES (3, 'Charlie', 2);
INSERT INTO departments VALUES (1, 'Engineering');
INSERT INTO departments VALUES (2, 'Sales');
SELECT e.name, d.dept_name FROM employees e JOIN departments d ON e.dept_id = d.id;`,
    },
    {
      title: 'CTE (WITH)',
      desc: 'Common Table Expressions for readable, reusable subqueries.',
      sql: `DROP DATABASE f_cte; CREATE DATABASE f_cte; USE f_cte;
CREATE TABLE employees (id INT PRIMARY KEY, name TEXT, dept_id INT);
CREATE TABLE departments (id INT PRIMARY KEY, dept_name TEXT);
INSERT INTO employees VALUES (1, 'Alice', 1);
INSERT INTO employees VALUES (2, 'Bob', 1);
INSERT INTO employees VALUES (3, 'Charlie', 2);
INSERT INTO departments VALUES (1, 'Engineering');
INSERT INTO departments VALUES (2, 'Sales');
WITH senior_employees AS (SELECT name, dept_id FROM employees WHERE id IN (1, 3))
SELECT e.name, d.dept_name FROM senior_employees e JOIN departments d ON e.dept_id = d.id;`,
    },
    {
      title: 'Window Functions',
      desc: 'Compute values across sets of rows without grouping.',
      sql: `DROP DATABASE f_window; CREATE DATABASE f_window; USE f_window;
CREATE TABLE sales (id INT PRIMARY KEY, rep TEXT, region TEXT, amount INT);
INSERT INTO sales VALUES (1, 'Alice', 'East', 100);
INSERT INTO sales VALUES (2, 'Bob', 'East', 200);
INSERT INTO sales VALUES (3, 'Charlie', 'West', 150);
INSERT INTO sales VALUES (4, 'Diana', 'West', 300);
INSERT INTO sales VALUES (5, 'Eve', 'East', 250);
SELECT rep, region, amount,
  ROW_NUMBER() OVER (PARTITION BY region ORDER BY amount DESC) AS rank,
  SUM(amount) OVER (PARTITION BY region) AS region_total,
  amount - LAG(amount) OVER (ORDER BY amount) AS diff
FROM sales;`,
    },
    {
      title: 'JSONB',
      desc: 'Store and query semi-structured JSON data with operators.',
      sql: `DROP DATABASE f_jsonb; CREATE DATABASE f_jsonb; USE f_jsonb;
CREATE TABLE events (id INT PRIMARY KEY, data JSONB);
INSERT INTO events VALUES (1, '{"type":"click","page":"/home","user":{"id":1,"name":"Alice"}}');
INSERT INTO events VALUES (2, '{"type":"view","page":"/products","user":{"id":2,"name":"Bob"}}');
SELECT data->>'type' AS event_type, data->>'page' AS page, data->'user'->>'name' AS user_name FROM events;`,
    },
    {
      title: 'UPSERT (ON CONFLICT)',
      desc: 'Insert or update in a single statement.',
      sql: `DROP DATABASE f_upsert; CREATE DATABASE f_upsert; USE f_upsert;
CREATE TABLE settings (name TEXT PRIMARY KEY, value TEXT);
INSERT INTO settings (name, value) VALUES ('theme', 'dark');
INSERT INTO settings (name, value) VALUES ('theme', 'light') ON CONFLICT DO UPDATE SET value = 'light';
SELECT * FROM settings;`,
    },
    {
      title: 'MERGE',
      desc: 'Conditional insert/update/delete in one statement.',
      sql: `DROP DATABASE f_merge; CREATE DATABASE f_merge; USE f_merge;
CREATE TABLE target (id INT PRIMARY KEY, val INT);
CREATE TABLE source (id INT PRIMARY KEY, val INT);
INSERT INTO target VALUES (1, 10);
INSERT INTO target VALUES (2, 20);
INSERT INTO source VALUES (2, 25);
INSERT INTO source VALUES (3, 30);
MERGE INTO target USING source ON target.id = source.id WHEN MATCHED THEN UPDATE SET val = source.val WHEN NOT MATCHED THEN INSERT (id, val) VALUES (source.id, source.val);
SELECT * FROM target;`,
    },
    {
      title: 'Indexes (BTree, GIN)',
      desc: 'Speed up queries with B-tree, hash, GIN, and GiST indexes.',
      sql: `DROP DATABASE f_index; CREATE DATABASE f_index; USE f_index;
CREATE TABLE products (id INT PRIMARY KEY, name TEXT, tags JSONB);
INSERT INTO products VALUES (1, 'Laptop', '["electronics","portable"]');
INSERT INTO products VALUES (2, 'Book', '["education"]');
CREATE INDEX idx_products_name ON products (name);
CREATE INDEX gin_idx_tags ON products (tags);
SELECT * FROM products WHERE name = 'Laptop';
SELECT * FROM products WHERE tags @> '["electronics"]';`,
    },
    {
      title: 'Transactions & MVCC',
      desc: 'ACID transactions with snapshot isolation (via TCP client on port 5432).',
      sql: `-- Transactions require TCP client (port 5432), not HTTP API.
-- Example session:
-- > BEGIN;
-- > UPDATE accounts SET balance = balance - 100 WHERE id = 1;
-- > UPDATE accounts SET balance = balance + 100 WHERE id = 2;
-- > COMMIT;
-- Use the Transaction Lab panel to test this.`,
    },
    {
      title: 'Aggregate Functions',
      desc: 'COUNT, SUM, AVG, MIN, MAX, STDDEV, VARIANCE, BOOL_AND/OR.',
      sql: `DROP DATABASE f_agg; CREATE DATABASE f_agg; USE f_agg;
CREATE TABLE scores (id INT PRIMARY KEY, student TEXT, subject TEXT, score INT);
INSERT INTO scores VALUES (1, 'Alice', 'Math', 95);
INSERT INTO scores VALUES (2, 'Bob', 'Math', 80);
INSERT INTO scores VALUES (3, 'Alice', 'Science', 90);
INSERT INTO scores VALUES (4, 'Bob', 'Science', 85);
SELECT subject, COUNT(*) AS cnt, AVG(score) AS avg_score, MAX(score) AS max_score, MIN(score) AS min_score FROM scores GROUP BY subject;`,
    },
    {
      title: 'LIKE & Full-Text',
      desc: 'Pattern matching with LIKE and full-text search via GIN index.',
      sql: `DROP DATABASE f_like; CREATE DATABASE f_like; USE f_like;
CREATE TABLE articles (id INT PRIMARY KEY, title TEXT, body TEXT);
INSERT INTO articles VALUES (1, 'Go concurrency', 'Goroutines and channels for parallel processing');
INSERT INTO articles VALUES (2, 'SQL optimization', 'Query planners and index selection strategies');
CREATE INDEX gin_body ON articles (body);
SELECT title FROM articles WHERE body LIKE '%parallel%';`,
    },
  ];

  function renderFeatureGrid() {
    const grid = $('#featureGrid');
    grid.innerHTML = features.map((f, i) =>
      `<div class="feature-card" data-idx="${i}"><h4>${esc(f.title)}</h4><p>${esc(f.desc)}</p></div>`
    ).join('');
    $$('.feature-card', grid).forEach(card => {
      card.addEventListener('click', () => {
        const f = features[card.dataset.idx];
        $('#featureGrid').style.display = 'none';
        $('#featureDemo').style.display = 'block';
        $('#featureTitle').textContent = f.title;
        $('#featureDesc').textContent = f.desc;
        $('#featureSql').textContent = f.sql;
        $('#featureResult').style.display = 'none';
        $('#featureResult')._db = f.db;
      });
    });
  }

  $('#featureBack').addEventListener('click', () => {
    $('#featureGrid').style.display = '';
    $('#featureDemo').style.display = 'none';
  });

  $('#featureRunBtn').addEventListener('click', async () => {
    const sql = $('#featureSql').textContent;
    const stmts = sql.split(';').filter(s => s.trim());
    let lastResult;
    let db = '';
    for (const stmt of stmts) {
      let trimmed = stmt.trim();
      if (!trimmed || trimmed.startsWith('--')) continue;
      const upper = trimmed.toUpperCase();
      if (upper.startsWith('USE ')) {
        db = trimmed.split(/\s+/)[1].replace(/;$/, '');
        lastResult = { status: 'ok', message: `Using database '${db}'` };
      } else {
        if (!trimmed.endsWith(';')) trimmed += ';';
        const result = await runQuery(trimmed, db);
        // Skip "already exists" and "does not exist" errors for idempotent setup
        if (result.status === 'error') {
          const msg = (result.message || '').toLowerCase();
          if (msg.includes('already exists') || msg.includes('does not exist') || msg.includes('index already')) {
            continue;
          }
          renderResult('featureResult', result);
          return;
        }
        lastResult = result;
        if (result.columns && result.rows) {
          renderResult('featureResult', result);
        }
      }
    }
    if (lastResult && !lastResult.columns) {
      renderResult('featureResult', lastResult);
    }
  });

  renderFeatureGrid();

  // --- Dashboard ---
  async function loadDashboard() {
    try {
      const resp = await fetch('/api/health');
      const h = await resp.json();
      $('#dashStatus').textContent = h.status || 'unknown';
      $('#dashStatus').style.color = h.status === 'ok' ? 'var(--accent)' : 'var(--err)';
      $('#dashVersion').textContent = h.version || '—';
      $('#dashUptime').textContent = h.uptime_s ? `${Math.floor(h.uptime_s / 60)}m ${h.uptime_s % 60}s` : '—';
      $('#dashConns').textContent = h.connections ?? '—';
    } catch {
      $('#dashStatus').textContent = 'offline';
      $('#dashStatus').style.color = 'var(--err)';
    }

    try {
      const resp = await fetch('/api/metrics');
      const text = await resp.text();
      $('#metricsRaw').textContent = text;
    } catch {
      $('#metricsRaw').textContent = 'Metrics endpoint unreachable';
    }
  }

  // --- Health check ---
  async function checkHealth() {
    try {
      const resp = await fetch('/api/health');
      const h = await resp.json();
      const dot = $('#statusDot');
      const txt = $('#statusText');
      if (h.status === 'ok' || h.status === 'degraded') {
        dot.classList.add('ok');
        txt.textContent = `VaultDB ${h.version || ''}`;
      } else {
        dot.classList.remove('ok');
        txt.textContent = 'Offline';
      }
    } catch {
      $('#statusDot').classList.remove('ok');
      $('#statusText').textContent = 'Offline';
    }
  }

  // --- Init ---
  loadDatabases();
  checkHealth();
  setInterval(checkHealth, 15000);

})();
