import React, { useState, useEffect } from 'react';
import { 
  Users, 
  Briefcase, 
  Search, 
  Terminal, 
  Cpu, 
  Plus, 
  Zap, 
  ShieldCheck, 
  Database,
  TrendingUp,
  RefreshCw
} from 'lucide-react';

const API_BASE = 'http://localhost:9090/api';

export default function App() {
  const [activeTab, setActiveTab] = useState('dashboard');
  const [customers, setCustomers] = useState([]);
  const [deals, setDeals] = useState([]);
  const [stats, setStats] = useState(null);
  const [searchQuery, setSearchQuery] = useState('');
  const [searchResults, setSearchResults] = useState([]);
  const [sqlQuery, setSqlQuery] = useState('SELECT id, name, company, score FROM crm_customers;');
  const [sqlResult, setSqlResult] = useState(null);
  const [sqlError, setSqlError] = useState(null);

  // New Customer Form
  const [newCustName, setNewCustName] = useState('');
  const [newCustEmail, setNewCustEmail] = useState('');
  const [newCustCompany, setNewCustCompany] = useState('');
  const [showAddModal, setShowAddModal] = useState(false);

  useEffect(() => {
    fetchStats();
    fetchCustomers();
    fetchDeals();
  }, []);

  const fetchStats = async () => {
    try {
      const res = await fetch(`${API_BASE}/stats`);
      const data = await res.json();
      setStats(data);
    } catch (e) {
      console.error(e);
    }
  };

  const fetchCustomers = async () => {
    try {
      const res = await fetch(`${API_BASE}/customers`);
      const data = await res.json();
      setCustomers(data || []);
    } catch (e) {
      console.error(e);
    }
  };

  const fetchDeals = async () => {
    try {
      const res = await fetch(`${API_BASE}/deals`);
      const data = await res.json();
      setDeals(data || []);
    } catch (e) {
      console.error(e);
    }
  };

  const handleAddCustomer = async (e) => {
    e.preventDefault();
    try {
      await fetch(`${API_BASE}/customers`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: newCustName,
          email: newCustEmail,
          company: newCustCompany,
          status: 'Active',
          metadata: '{"source":"Web Form"}'
        })
      });
      setShowAddModal(false);
      setNewCustName('');
      setNewCustEmail('');
      setNewCustCompany('');
      fetchCustomers();
      fetchStats();
    } catch (err) {
      alert(err.message);
    }
  };

  const handleUpdateDealStage = async (id, stage) => {
    try {
      await fetch(`${API_BASE}/deals`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id, stage, probability: stage === 'Closed Won' ? 1.0 : 0.7 })
      });
      fetchDeals();
    } catch (err) {
      alert(err.message);
    }
  };

  const handleSearchNotes = async () => {
    if (!searchQuery) return;
    try {
      const res = await fetch(`${API_BASE}/notes/search?q=${encodeURIComponent(searchQuery)}`);
      const data = await res.json();
      setSearchResults(data || []);
    } catch (e) {
      console.error(e);
    }
  };

  const handleRunSQL = async () => {
    setSqlError(null);
    setSqlResult(null);
    try {
      const res = await fetch(`${API_BASE}/sql`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ query: sqlQuery })
      });
      const data = await res.json();
      if (data.error) {
        setSqlError(data.error);
      } else {
        setSqlResult(data);
      }
    } catch (e) {
      setSqlError(e.message);
    }
  };

  return (
    <div className="app-container">
      {/* Sidebar */}
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-logo">V</div>
          <div>
            <div className="brand-title">VaultCRM</div>
            <div style={{ fontSize: '11px', color: 'var(--accent-cyan)', fontWeight: 600 }}>POWERED BY VAULTDB</div>
          </div>
        </div>

        <ul className="nav-list">
          <li className={`nav-item ${activeTab === 'dashboard' ? 'active' : ''}`} onClick={() => setActiveTab('dashboard')}>
            <Cpu size={18} /> Обзор системы
          </li>
          <li className={`nav-item ${activeTab === 'customers' ? 'active' : ''}`} onClick={() => setActiveTab('customers')}>
            <Users size={18} /> Клиенты (DML)
          </li>
          <li className={`nav-item ${activeTab === 'deals' ? 'active' : ''}`} onClick={() => setActiveTab('deals')}>
            <Briefcase size={18} /> Сделки (HOT Updates)
          </li>
          <li className={`nav-item ${activeTab === 'fts' ? 'active' : ''}`} onClick={() => setActiveTab('fts')}>
            <Search size={18} /> Поиск (GIN FTS)
          </li>
          <li className={`nav-item ${activeTab === 'sql' ? 'active' : ''}`} onClick={() => setActiveTab('sql')}>
            <Terminal size={18} /> SQL Консоль
          </li>
          <li className={`nav-item ${activeTab === 'inspector' ? 'active' : ''}`} onClick={() => setActiveTab('inspector')}>
            <Database size={18} /> VaultDB Inspector
          </li>
        </ul>
      </aside>

      {/* Main Content */}
      <main className="main-content">
        {/* Header */}
        <header className="header">
          <div>
            <h1 className="header-title">
              {activeTab === 'dashboard' && 'Обзор системы VaultDB & CRM'}
              {activeTab === 'customers' && 'Управление клиентами'}
              {activeTab === 'deals' && 'Воронка сделок (HOT updates)'}
              {activeTab === 'fts' && 'Полнотекстовый поиск (GIN Index)'}
              {activeTab === 'sql' && 'Интерактивная SQL консоль'}
              {activeTab === 'inspector' && 'Системный инспектор VaultDB'}
            </h1>
            <p className="header-subtitle">Тестовый комплекс высокой нагрузки с поддержкой ACID и Zero-Alloc</p>
          </div>
          <button className="btn btn-primary" onClick={() => { fetchStats(); fetchCustomers(); fetchDeals(); }}>
            <RefreshCw size={16} /> Обновить
          </button>
        </header>

        {/* DASHBOARD TAB */}
        {activeTab === 'dashboard' && (
          <div>
            <div className="grid-metrics">
              <div className="card metric-card">
                <span className="metric-title">Всего клиентов</span>
                <span className="metric-value">{stats?.total_customers || 0}</span>
                <span className="metric-badge">Zero-Alloc Read</span>
              </div>
              <div className="card metric-card">
                <span className="metric-title">Активных сделок</span>
                <span className="metric-value">{stats?.total_deals || 0}</span>
                <span className="metric-badge">HOT Enabled</span>
              </div>
              <div className="card metric-card">
                <span className="metric-title">Статус Raft Кластера</span>
                <span className="metric-value" style={{ fontSize: '20px', color: 'var(--accent-emerald)' }}>{stats?.raft_status || 'Leader'}</span>
                <span className="metric-badge">Consensus Active</span>
              </div>
              <div className="card metric-card">
                <span className="metric-title">Режим движка</span>
                <span className="metric-value" style={{ fontSize: '18px', color: 'var(--accent-cyan)' }}>{stats?.engine_mode || 'Mmap / Zero-Alloc'}</span>
                <span className="metric-badge">High Performance</span>
              </div>
            </div>

            <div className="card" style={{ marginTop: '24px' }}>
              <h3 style={{ marginBottom: '16px', fontFamily: 'var(--font-heading)' }}>Архитектурное соответствие VaultDB</h3>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))', gap: '16px' }}>
                <div style={{ padding: '16px', background: 'rgba(255,255,255,0.02)', borderRadius: '12px', border: '1px solid var(--border-color)' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '8px', fontWeight: 600, color: 'var(--accent-cyan)' }}>
                    <Zap size={18} /> Zero-Allocation Hot Path
                  </div>
                  <p style={{ fontSize: '13px', color: 'var(--text-muted)', marginTop: '8px' }}>
                    Использование AllocRow и rowPools переиспользует слайсы объектов, предотвращая паузы сборщика мусора.
                  </p>
                </div>
                <div style={{ padding: '16px', background: 'rgba(255,255,255,0.02)', borderRadius: '12px', border: '1px solid var(--border-color)' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '8px', fontWeight: 600, color: 'var(--accent-emerald)' }}>
                    <TrendingUp size={18} /> Heap-Only Tuples (HOT)
                  </div>
                  <p style={{ fontSize: '13px', color: 'var(--text-muted)', marginTop: '8px' }}>
                    Обновления статуса сделок (UPDATE) выполняются локально на странице без перестроения B-Tree индексов.
                  </p>
                </div>
                <div style={{ padding: '16px', background: 'rgba(255,255,255,0.02)', borderRadius: '12px', border: '1px solid var(--border-color)' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '8px', fontWeight: 600, color: 'var(--accent-secondary)' }}>
                    <ShieldCheck size={18} /> GIN & Full-Text Search
                  </div>
                  <p style={{ fontSize: '13px', color: 'var(--text-muted)', marginTop: '8px' }}>
                    Инвертированный индекс GIN обеспечивает мгновенный полнотекстовый поиск по содержимому заметок клиентов.
                  </p>
                </div>
              </div>
            </div>
          </div>
        )}

        {/* CUSTOMERS TAB */}
        {activeTab === 'customers' && (
          <div className="card">
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '20px' }}>
              <h3>Список клиентов (Таблица crm_customers)</h3>
              <button className="btn btn-primary" onClick={() => setShowAddModal(true)}>
                <Plus size={16} /> Добавить клиента (WASM Scoring)
              </button>
            </div>

            <div className="table-container">
              <table>
                <thead>
                  <tr>
                    <th>ID</th>
                    <th>Имя</th>
                    <th>Email</th>
                    <th>Компания</th>
                    <th>Статус</th>
                    <th>WASM Lead Score</th>
                    <th>Metadata (JSONB)</th>
                  </tr>
                </thead>
                <tbody>
                  {customers.map((c) => (
                    <tr key={c.id}>
                      <td>#{c.id}</td>
                      <td style={{ fontWeight: 600 }}>{c.name}</td>
                      <td>{c.email}</td>
                      <td>{c.company}</td>
                      <td>
                        <span className={`status-badge ${c.status === 'Active' ? 'status-active' : c.status === 'VIP' ? 'status-vip' : 'status-lead'}`}>
                          {c.status}
                        </span>
                      </td>
                      <td style={{ fontWeight: 700, color: 'var(--accent-cyan)' }}>{c.score} / 100</td>
                      <td style={{ fontFamily: 'var(--font-mono)', fontSize: '12px', color: 'var(--text-muted)' }}>{c.metadata}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {/* Modal */}
            {showAddModal && (
              <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.7)', backdropFilter: 'blur(5px)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 100 }}>
                <div className="card" style={{ width: '420px' }}>
                  <h3 style={{ marginBottom: '16px' }}>Новый клиент</h3>
                  <form onSubmit={handleAddCustomer} style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
                    <input className="btn" style={{ background: '#182030', color: '#fff', border: '1px solid var(--border-color)' }} placeholder="ФИО" value={newCustName} onChange={e => setNewCustName(e.target.value)} required />
                    <input className="btn" style={{ background: '#182030', color: '#fff', border: '1px solid var(--border-color)' }} placeholder="Email" value={newCustEmail} onChange={e => setNewCustEmail(e.target.value)} required />
                    <input className="btn" style={{ background: '#182030', color: '#fff', border: '1px solid var(--border-color)' }} placeholder="Компания" value={newCustCompany} onChange={e => setNewCustCompany(e.target.value)} required />
                    <div style={{ display: 'flex', gap: '10px', justifyContent: 'flex-end', marginTop: '12px' }}>
                      <button type="button" className="btn" onClick={() => setShowAddModal(false)}>Отмена</button>
                      <button type="submit" className="btn btn-primary">Сохранить</button>
                    </div>
                  </form>
                </div>
              </div>
            )}
          </div>
        )}

        {/* DEALS TAB */}
        {activeTab === 'deals' && (
          <div className="card">
            <h3 style={{ marginBottom: '20px' }}>Управление сделками (Тестирование HOT-обновлений в VaultDB)</h3>
            <div className="table-container">
              <table>
                <thead>
                  <tr>
                    <th>ID</th>
                    <th>Название сделки</th>
                    <th>Сумма</th>
                    <th>Текущий этап</th>
                    <th>Вероятность</th>
                    <th>Действие (HOT UPDATE)</th>
                  </tr>
                </thead>
                <tbody>
                  {deals.map((d) => (
                    <tr key={d.id}>
                      <td>#{d.id}</td>
                      <td style={{ fontWeight: 600 }}>{d.title}</td>
                      <td>{d.amount.toLocaleString()} ₽</td>
                      <td>
                        <span className="status-badge status-active">{d.stage}</span>
                      </td>
                      <td>{(d.probability * 100).toFixed(0)}%</td>
                      <td>
                        <div style={{ display: 'flex', gap: '8px' }}>
                          <button className="btn" style={{ padding: '4px 10px', fontSize: '12px', background: 'rgba(99,102,241,0.2)', color: '#A5B4FC' }} onClick={() => handleUpdateDealStage(d.id, 'Negotiation')}>
                            Переговоры
                          </button>
                          <button className="btn" style={{ padding: '4px 10px', fontSize: '12px', background: 'rgba(16,185,129,0.2)', color: '#6EE7B7' }} onClick={() => handleUpdateDealStage(d.id, 'Closed Won')}>
                            Закрыта (Успешно)
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}

        {/* FTS SEARCH TAB */}
        {activeTab === 'fts' && (
          <div className="card">
            <h3 style={{ marginBottom: '16px' }}>Полнотекстовый поиск с GIN-индексом</h3>
            <div style={{ display: 'flex', gap: '12px', marginBottom: '24px' }}>
              <input 
                className="btn" 
                style={{ flex: 1, background: '#182030', color: '#fff', border: '1px solid var(--border-color)' }}
                placeholder="Введите ключевое слово (например: Raft, HOT, Поддержка)..."
                value={searchQuery}
                onChange={e => setSearchQuery(e.target.value)}
              />
              <button className="btn btn-primary" onClick={handleSearchNotes}>
                <Search size={16} /> Найти по GIN
              </button>
            </div>

            <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
              {searchResults.map((n) => (
                <div key={n.id} style={{ padding: '16px', background: 'rgba(255,255,255,0.02)', borderRadius: '12px', border: '1px solid var(--border-color)' }}>
                  <div style={{ fontSize: '12px', color: 'var(--accent-cyan)', marginBottom: '4px' }}>Автор: {n.author} • {n.created_at}</div>
                  <div style={{ fontSize: '14px', color: '#E5E7EB' }}>{n.content}</div>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* SQL CONSOLE TAB */}
        {activeTab === 'sql' && (
          <div className="card">
            <h3 style={{ marginBottom: '16px' }}>VaultDB Interactive SQL Console</h3>
            <textarea 
              className="code-console" 
              style={{ width: '100%', minHeight: '100px', marginBottom: '16px' }}
              value={sqlQuery}
              onChange={e => setSqlQuery(e.target.value)}
            />
            <button className="btn btn-primary" onClick={handleRunSQL}>
              <Zap size={16} /> Выполнить SQL
            </button>

            {sqlError && (
              <div style={{ marginTop: '16px', padding: '12px', background: 'rgba(239, 68, 68, 0.2)', border: '1px solid #EF4444', color: '#FCA5A5', borderRadius: '8px' }}>
                Ошибка: {sqlError}
              </div>
            )}

            {sqlResult && (
              <div style={{ marginTop: '20px' }}>
                <h4 style={{ marginBottom: '10px' }}>Результат (Затронуто строк: {sqlResult.affected_rows})</h4>
                <div className="code-console" style={{ maxHeight: '300px', overflowY: 'auto' }}>
                  <pre>{JSON.stringify(sqlResult.rows, null, 2)}</pre>
                </div>
              </div>
            )}
          </div>
        )}

        {/* VAULTDB INSPECTOR TAB */}
        {activeTab === 'inspector' && (
          <div className="card">
            <h3 style={{ marginBottom: '20px' }}>Детальная инспекция внутреннего движка VaultDB</h3>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))', gap: '16px' }}>
              <div style={{ padding: '16px', background: 'rgba(255,255,255,0.02)', borderRadius: '12px', border: '1px solid var(--border-color)' }}>
                <h4 style={{ color: 'var(--accent-primary)', marginBottom: '8px' }}>Page Storage Engine</h4>
                <p style={{ fontSize: '13px', color: 'var(--text-muted)' }}>
                  • Разрядность страницы: 8192 байт<br/>
                  • Схема адресации: Slotted Page Header<br/>
                  • Режим работы: Memory Mapped Buffer Pool
                </p>
              </div>
              <div style={{ padding: '16px', background: 'rgba(255,255,255,0.02)', borderRadius: '12px', border: '1px solid var(--border-color)' }}>
                <h4 style={{ color: 'var(--accent-emerald)', marginBottom: '8px' }}>Transaction Manager</h4>
                <p style={{ fontSize: '13px', color: 'var(--text-muted)' }}>
                  • Уровень изоляции: MVCC / SSI<br/>
                  • Предикатные блокировки: Active<br/>
                  • Потокобезопасность: LWLocks & RWMutex
                </p>
              </div>
            </div>
          </div>
        )}
      </main>
    </div>
  );
}
