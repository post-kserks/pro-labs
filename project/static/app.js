// DocVault Web UI — Frontend Logic

const API = '';

// Navigation
document.querySelectorAll('.nav-btn').forEach(btn => {
    btn.addEventListener('click', () => {
        document.querySelectorAll('.nav-btn').forEach(b => b.classList.remove('active'));
        document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
        btn.classList.add('active');
        document.getElementById(`view-${btn.dataset.view}`).classList.add('active');
        if (btn.dataset.view === 'stats') loadStats();
        if (btn.dataset.view === 'audit') loadAudit();
    });
});

// Documents List
async function loadDocuments(query = '', department = '') {
    const resp = await fetch(`${API}/api/search`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ query, department })
    });
    const data = await resp.json();
    renderDocuments(data.documents || []);
}

function renderDocuments(docs) {
    const container = document.getElementById('documents-list');
    if (!docs.length) {
        container.innerHTML = '<div class="empty-state"><p>Документы не найдены</p></div>';
        return;
    }
    container.innerHTML = `
        <table>
            <thead>
                <tr>
                    <th>Номер</th>
                    <th>Название</th>
                    <th>Отдел</th>
                    <th>Автор</th>
                    <th>Размер</th>
                    <th>Статус</th>
                    <th>Дата</th>
                    <th></th>
                </tr>
            </thead>
            <tbody>
                ${docs.map(d => `
                    <tr class="clickable" onclick="showDocument(${d.id})">
                        <td><strong>${d.doc_number}</strong></td>
                        <td>${d.title}</td>
                        <td>${d.department}</td>
                        <td>${d.author}</td>
                        <td>${formatSize(d.file_size)}</td>
                        <td><span class="status-badge status-${d.status}">${d.status}</span></td>
                        <td>${formatDate(d.created_at)}</td>
                        <td>
                            <button class="btn btn-sm" onclick="event.stopPropagation(); editDocument(${d.id})">Ред.</button>
                            <button class="btn btn-sm btn-danger" onclick="event.stopPropagation(); deleteDocument(${d.id})">Удал.</button>
                        </td>
                    </tr>
                `).join('')}
            </tbody>
        </table>
    `;
}

// Search
document.getElementById('btn-search').addEventListener('click', () => {
    const query = document.getElementById('search-input').value;
    const dept = document.getElementById('filter-department').value;
    // If only department filter, pass empty query to get all + filter
    if (!query && dept) {
        loadDocuments('', dept);
    } else if (query) {
        loadDocuments(query, dept);
    } else {
        loadDocuments();
    }
});

document.getElementById('search-input').addEventListener('keyup', (e) => {
    if (e.key === 'Enter') document.getElementById('btn-search').click();
});

// Document Detail
async function showDocument(id) {
    const resp = await fetch(`${API}/api/document?id=${id}`);
    const data = await resp.json();
    const doc = data.document;
    if (!doc) return;

    document.getElementById('detail-title').textContent = doc.title;
    document.getElementById('detail-body').innerHTML = `
        <div class="detail-field">
            <div class="label">Номер</div>
            <div class="value">${doc.doc_number}</div>
        </div>
        <div class="detail-field">
            <div class="label">Статус</div>
            <div class="value"><span class="status-badge status-${doc.status}">${doc.status}</span></div>
        </div>
        <div class="detail-field">
            <div class="label">Отдел</div>
            <div class="value">${doc.department}</div>
        </div>
        <div class="detail-field">
            <div class="label">Автор</div>
            <div class="value">${doc.author}</div>
        </div>
        <div class="detail-field">
            <div class="label">Размер</div>
            <div class="value">${formatSize(doc.file_size)}</div>
        </div>
        <div class="detail-field">
            <div class="label">Создан</div>
            <div class="value">${formatDate(doc.created_at)}</div>
        </div>
        <div class="detail-field">
            <div class="label">Содержание</div>
            <div class="detail-content">${doc.content || '(пусто)'}</div>
        </div>
        <div class="versions-list" id="versions-container"></div>
    `;
    document.getElementById('modal-detail').style.display = 'flex';
    loadVersions(id);
}

async function loadVersions(docId) {
    const resp = await fetch(`${API}/api/versions?doc_id=${docId}`);
    const data = await resp.json();
    const container = document.getElementById('versions-container');
    if (!data.versions || !data.versions.length) {
        container.innerHTML = '<h3>Версии</h3><p style="color:#999">Нет версий</p>';
        return;
    }
    container.innerHTML = `
        <h3>Версии (${data.versions.length})</h3>
        <table>
            <thead>
                <tr><th>Версия</th><th>Описание</th><th>Автор</th><th>Дата</th></tr>
            </thead>
            <tbody>
                ${data.versions.map(v => `
                    <tr>
                        <td>v${v.version_number}</td>
                        <td>${v.changes_description}</td>
                        <td>${v.changed_by}</td>
                        <td>${formatDate(v.changed_at)}</td>
                    </tr>
                `).join('')}
            </tbody>
        </table>
    `;
}

// Create / Edit Document
document.getElementById('btn-create').addEventListener('click', () => {
    document.getElementById('form-title').textContent = 'Новый документ';
    document.getElementById('form-id').value = '';
    document.getElementById('form-doc-number').value = '';
    document.getElementById('form-title-input').value = '';
    document.getElementById('form-content').value = '';
    document.getElementById('form-department').value = 'legal';
    document.getElementById('form-author').value = '';
    document.getElementById('modal-form').style.display = 'flex';
});

async function editDocument(id) {
    const resp = await fetch(`${API}/api/document?id=${id}`);
    const data = await resp.json();
    const doc = data.document;
    if (!doc) return;
    document.getElementById('form-title').textContent = 'Редактировать документ';
    document.getElementById('form-id').value = doc.id;
    document.getElementById('form-doc-number').value = doc.doc_number;
    document.getElementById('form-title-input').value = doc.title;
    document.getElementById('form-content').value = doc.content || '';
    document.getElementById('form-department').value = doc.department;
    document.getElementById('form-author').value = doc.author;
    document.getElementById('modal-form').style.display = 'flex';
}

document.getElementById('document-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const id = document.getElementById('form-id').value;
    const body = {
        doc_number: document.getElementById('form-doc-number').value,
        title: document.getElementById('form-title-input').value,
        content: document.getElementById('form-content').value,
        department: document.getElementById('form-department').value,
        author: document.getElementById('form-author').value,
        file_size: document.getElementById('form-content').value.length
    };
    if (id) body.id = parseInt(id);

    const url = id ? '/api/document/update' : '/api/document';
    const resp = await fetch(`${API}${url}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
    });
    const result = await resp.json();
    if (result.status === 'created' || result.status === 'updated') {
        closeModal('modal-form');
        loadDocuments();
    } else {
        alert('Ошибка: ' + (result.error || 'unknown'));
    }
});

// Delete Document
async function deleteDocument(id) {
    if (!confirm('Удалить документ?')) return;
    const resp = await fetch(`${API}/api/document/delete`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id })
    });
    const result = await resp.json();
    if (result.status === 'deleted') loadDocuments();
}

// Stats
async function loadStats() {
    const resp = await fetch(`${API}/api/stats`);
    const data = await resp.json();
    const container = document.getElementById('stats-content');
    const depts = Object.entries(data.by_department || {});
    container.innerHTML = `
        <div class="stat-card">
            <div class="value">${data.total_documents}</div>
            <div class="label">Всего документов</div>
        </div>
        <div class="stat-card">
            <div class="value">${data.total_versions}</div>
            <div class="label">Версий документов</div>
        </div>
        ${depts.map(([dept, count]) => `
            <div class="stat-card">
                <div class="value">${count}</div>
                <div class="label">${dept}</div>
            </div>
        `).join('')}
    `;
}

// Audit Log
async function loadAudit() {
    const resp = await fetch(`${API}/api/audit`);
    const data = await resp.json();
    const container = document.getElementById('audit-content');
    const entries = data.audit || [];
    if (!entries.length) {
        container.innerHTML = '<div class="empty-state"><p>Аудит-лог пуст (Enterprise feature)</p></div>';
        return;
    }
    container.innerHTML = `
        <table>
            <thead>
                <tr><th>Пользователь</th><th>Действие</th><th>Объект</th><th>Детали</th><th>Дата</th></tr>
            </thead>
            <tbody>
                ${entries.map(e => `
                    <tr>
                        <td>${e.username || '-'}</td>
                        <td>${e.action}</td>
                        <td>${e.object_type || '-'}</td>
                        <td>${e.details || '-'}</td>
                        <td>${formatDate(e.occurred_at)}</td>
                    </tr>
                `).join('')}
            </tbody>
        </table>
    `;
}

// Helpers
function closeModal(id) {
    document.getElementById(id).style.display = 'none';
}

function formatSize(bytes) {
    if (!bytes) return '-';
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
}

function formatDate(ts) {
    if (!ts || ts === 'NULL') return '-';
    return ts.replace('T', ' ').replace('Z', '');
}

// Init
loadDocuments();
