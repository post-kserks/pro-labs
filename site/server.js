const express = require('express');
const http = require('http');
const path = require('path');

const app = express();
const PORT = process.env.PORT || 3000;
const VAULTDB_HOST = process.env.VAULTDB_HOST || '127.0.0.1';
const VAULTDB_HTTP_PORT = process.env.VAULTDB_HTTP_PORT || 8080;

app.use(express.json());
app.use(express.static(path.join(__dirname, 'public')));

app.post('/api/query', async (req, res) => {
  const { database, query } = req.body;
  try {
    const data = JSON.stringify({ database, query });
    const options = {
      hostname: VAULTDB_HOST,
      port: VAULTDB_HTTP_PORT,
      path: '/api/query',
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Content-Length': Buffer.byteLength(data),
      },
    };
    const proxyReq = http.request(options, (proxyRes) => {
      let body = '';
      proxyRes.on('data', (chunk) => { body += chunk; });
      proxyRes.on('end', () => {
        try {
          res.json(JSON.parse(body));
        } catch {
          res.status(500).json({ status: 'error', message: 'Invalid response from VaultDB' });
        }
      });
    });
    proxyReq.on('error', (err) => {
      res.status(502).json({ status: 'error', message: `VaultDB unreachable: ${err.message}` });
    });
    proxyReq.write(data);
    proxyReq.end();
  } catch (err) {
    res.status(500).json({ status: 'error', message: err.message });
  }
});

app.get('/api/health', async (req, res) => {
  const options = {
    hostname: VAULTDB_HOST,
    port: VAULTDB_HTTP_PORT,
    path: '/health',
    method: 'GET',
  };
  const proxyReq = http.request(options, (proxyRes) => {
    let body = '';
    proxyRes.on('data', (chunk) => { body += chunk; });
    proxyRes.on('end', () => {
      try {
        res.json(JSON.parse(body));
      } catch {
        res.json({ status: 'offline' });
      }
    });
  });
  proxyReq.on('error', () => {
    res.json({ status: 'offline' });
  });
  proxyReq.end();
});

app.get('/api/databases', async (req, res) => {
  const options = {
    hostname: VAULTDB_HOST,
    port: VAULTDB_HTTP_PORT,
    path: '/api/databases',
    method: 'GET',
  };
  const proxyReq = http.request(options, (proxyRes) => {
    let body = '';
    proxyRes.on('data', (chunk) => { body += chunk; });
    proxyRes.on('end', () => {
      try {
        res.json(JSON.parse(body));
      } catch {
        res.json({ databases: [] });
      }
    });
  });
  proxyReq.on('error', () => {
    res.json({ databases: [] });
  });
  proxyReq.end();
});

// Proxy for /api/databases/:db/tables and /api/databases/:db/tables/:table/*
app.get('/api/databases/:db/tables/:table/schema', async (req, res) => {
  const { db, table } = req.params;
  const options = {
    hostname: VAULTDB_HOST,
    port: VAULTDB_HTTP_PORT,
    path: `/api/databases/${db}/tables/${table}/schema`,
    method: 'GET',
  };
  const proxyReq = http.request(options, (proxyRes) => {
    let body = '';
    proxyRes.on('data', (chunk) => { body += chunk; });
    proxyRes.on('end', () => {
      try {
        res.json(JSON.parse(body));
      } catch {
        res.json({ status: 'error', message: 'Invalid response' });
      }
    });
  });
  proxyReq.on('error', (err) => {
    res.status(502).json({ status: 'error', message: err.message });
  });
  proxyReq.end();
});

app.get('/api/databases/:db/tables', async (req, res) => {
  const { db } = req.params;
  const options = {
    hostname: VAULTDB_HOST,
    port: VAULTDB_HTTP_PORT,
    path: `/api/databases/${db}/tables`,
    method: 'GET',
  };
  const proxyReq = http.request(options, (proxyRes) => {
    let body = '';
    proxyRes.on('data', (chunk) => { body += chunk; });
    proxyRes.on('end', () => {
      try {
        res.json(JSON.parse(body));
      } catch {
        res.json({ tables: [] });
      }
    });
  });
  proxyReq.on('error', () => {
    res.json({ tables: [] });
  });
  proxyReq.end();
});

app.get('/api/metrics', async (req, res) => {
  const options = {
    hostname: VAULTDB_HOST,
    port: parseInt(process.env.VAULTDB_MONITOR_PORT || '5433'),
    path: '/metrics',
    method: 'GET',
  };
  const proxyReq = http.request(options, (proxyRes) => {
    let body = '';
    proxyRes.on('data', (chunk) => { body += chunk; });
    proxyRes.on('end', () => {
      res.set('Content-Type', 'text/plain');
      res.send(body);
    });
  });
  proxyReq.on('error', () => {
    res.status(502).send('Metrics endpoint unreachable');
  });
  proxyReq.end();
});

app.listen(PORT, () => {
  console.log(`VaultDB Lab running at http://localhost:${PORT}`);
  console.log(`Proxying to VaultDB at ${VAULTDB_HOST}:${VAULTDB_HTTP_PORT}`);
});
