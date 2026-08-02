// Lógica do Painel Administrativo do Restaurante (Lojista)

let adminToken = localStorage.getItem('admin_token');
let currentTab = 'pedidos';
let menuItems = [];
let orders = [];
let editingItemId = null;

function formatCurrency(val) {
  return new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' }).format(val);
}

// Utilitário para mostrar Toasts
function showToast(message, type = 'info') {
  const container = document.getElementById('toast-container');
  const toast = document.createElement('div');
  toast.className = `toast toast-${type} glass`;
  toast.innerHTML = `<span>${message}</span>`;
  container.appendChild(toast);
  
  setTimeout(() => toast.classList.add('show'), 50);
  
  setTimeout(() => {
    toast.classList.remove('show');
    setTimeout(() => toast.remove(), 300);
  }, 3500);
}

document.addEventListener('DOMContentLoaded', () => {
  checkAuth();
  setupEventListeners();
});

// Verifica se está logado; se não estiver, abre overlay de login
function checkAuth() {
  adminToken = localStorage.getItem('admin_token');
  if (!adminToken) {
    document.getElementById('login-overlay').classList.add('open');
  } else {
    document.getElementById('login-overlay').classList.remove('open');
    document.getElementById('logged-user-name').innerText = localStorage.getItem('admin_name') || 'Lojista';
    loadDashboardData();
    // Inicia Polling de Pedidos a cada 10 segundos
    setInterval(loadOrders, 10000);
  }
}

// Lógica de Login
async function handleLogin(e) {
  e.preventDefault();
  const identifier = document.getElementById('login-slug').value.trim();
  const pass = document.getElementById('login-password').value.trim();

  try {
    const res = await fetch('/api/tenant/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ identifier, password: pass })
    });

    const data = await res.json();
    if (!res.ok) throw new Error(data.error || 'Credenciais inválidas.');

    localStorage.setItem('admin_token', data.token);
    localStorage.setItem('admin_name', data.nome);
    localStorage.setItem('admin_slug', data.slug);
    
    showToast(`Bem-vindo, ${data.nome}!`, 'success');
    checkAuth();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

function handleLogout() {
  localStorage.removeItem('admin_token');
  localStorage.removeItem('admin_name');
  localStorage.removeItem('admin_slug');
  window.location.reload();
}

// Carrega os dados da tela ativa
function loadDashboardData() {
  loadOrders();
  loadMenu();
  loadPaymentsConfig();
  loadGeneralConfig();
}

// Tab switcher
function switchTab(tab) {
  currentTab = tab;
  document.querySelectorAll('.nav-item').forEach(item => item.classList.remove('active'));
  document.getElementById(`nav-${tab}`).classList.add('active');

  document.getElementById('tab-pedidos').style.display = tab === 'pedidos' ? 'block' : 'none';
  document.getElementById('tab-cardapio').style.display = tab === 'cardapio' ? 'block' : 'none';
  document.getElementById('tab-pagamentos').style.display = tab === 'pagamentos' ? 'block' : 'none';
  document.getElementById('tab-usuarios').style.display = tab === 'usuarios' ? 'block' : 'none';
  document.getElementById('tab-configuracoes').style.display = tab === 'configuracoes' ? 'block' : 'none';

  if (tab === 'usuarios') {
    loadUsers();
  } else if (tab === 'configuracoes') {
    loadGeneralConfig();
  }
}

// --- GESTÃO DE PEDIDOS ---
async function loadOrders() {
  if (!adminToken) return;
  try {
    const res = await fetch('/api/admin/pedidos', {
      headers: { 'Authorization': adminToken }
    });
    if (!res.ok) throw new Error();
    
    orders = await res.json();
    renderOrdersBoard();
    renderMetrics();
  } catch (err) {
    showToast('Falha ao sincronizar pedidos.', 'error');
  }
}

function renderMetrics() {
  const totalSales = orders.filter(o => o.status === 'finalizado').reduce((sum, o) => sum + o.valor_total, 0);
  const activeOrders = orders.filter(o => o.status !== 'finalizado' && o.status !== 'cancelado').length;
  
  document.getElementById('metric-sales').innerText = formatCurrency(totalSales);
  document.getElementById('metric-orders').innerText = activeOrders;
  document.getElementById('metric-total-orders').innerText = orders.length;
}

function renderOrdersBoard() {
  const cols = {
    pendente: document.getElementById('list-pendente'),
    preparando: document.getElementById('list-preparando'),
    pronto: document.getElementById('list-pronto'),
    finalizado: document.getElementById('list-finalizado')
  };

  // Limpa colunas
  Object.values(cols).forEach(c => c.innerHTML = '');
  
  // Contadores
  const counts = { pendente: 0, preparando: 0, pronto: 0, finalizado: 0 };

  orders.forEach(order => {
    // Agrupa pedidos finalizados/cancelados na coluna de histórico
    const colName = order.status === 'cancelado' ? 'finalizado' : order.status;
    const col = cols[colName];
    if (!col) return;
    
    counts[colName]++;

    const card = document.createElement('div');
    card.className = 'order-card glass';
    if (order.status === 'cancelado') card.style.borderLeftColor = 'var(--danger)';
    else if (order.status === 'pronto') card.style.borderLeftColor = 'var(--success)';
    else if (order.status === 'preparando') card.style.borderLeftColor = 'var(--warning)';
    else card.style.borderLeftColor = 'var(--primary)';

    const paymentLabel = order.forma_pagamento.toUpperCase().replace('_', ' ');

    let itemsRows = order.itens.map(i => `
      <div class="order-item-row">
        <span>${i.quantidade}x ${i.nome}</span>
        <span>${formatCurrency(i.preco_unitario * i.quantidade)}</span>
      </div>
    `).join('');

    let actionButtons = '';
    if (order.status === 'pendente') {
      actionButtons = `
        <div class="order-actions">
          <button class="btn btn-primary order-btn" onclick="updateOrderStatus(${order.id}, 'preparando')">Aceitar e Preparar</button>
          <button class="btn btn-secondary order-btn" onclick="updateOrderStatus(${order.id}, 'cancelado')" style="color:var(--danger)">Recusar</button>
        </div>
      `;
    } else if (order.status === 'preparando') {
      actionButtons = `
        <div class="order-actions">
          <button class="btn btn-primary order-btn" onclick="updateOrderStatus(${order.id}, 'pronto')" style="background:var(--success)">Pronto para Retirada</button>
        </div>
      `;
    } else if (order.status === 'pronto') {
      actionButtons = `
        <div class="order-actions">
          <button class="btn btn-primary order-btn" onclick="updateOrderStatus(${order.id}, 'finalizado')" style="background:var(--info)">Entregue / Retirado</button>
        </div>
      `;
    }

    const cancelBadge = order.status === 'cancelado' 
      ? `<span style="color:var(--danger); font-weight:700;">[RECUSADO/CANCELADO]</span>` 
      : '';

    card.innerHTML = `
      <div class="order-header">
        <span class="order-id">Pedido ID #${order.id}</span>
        <span class="order-time">${new Date(order.created_at).toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit' })}</span>
      </div>
      <div class="order-client">
        ${order.cliente_nome}
        <div class="order-phone">${order.cliente_telefone}</div>
      </div>
      <div class="order-items">
        ${itemsRows}
      </div>
      <div class="order-footer">
        <span class="order-price">${formatCurrency(order.valor_total)}</span>
        <span class="order-payment">${paymentLabel}</span>
      </div>
      ${cancelBadge}
      ${actionButtons}
    `;

    col.appendChild(card);
  });

  // Atualiza contadores
  Object.keys(counts).forEach(k => {
    document.getElementById(`count-${k}`).innerText = counts[k];
  });
}

async function updateOrderStatus(orderId, newStatus) {
  try {
    const res = await fetch(`/api/admin/pedidos/${orderId}/status`, {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': adminToken
      },
      body: JSON.stringify({ status: newStatus })
    });
    if (!res.ok) throw new Error();
    
    showToast('Status do pedido atualizado!', 'success');
    loadOrders();
  } catch (err) {
    showToast('Erro ao atualizar status do pedido.', 'error');
  }
}

// --- GESTÃO DE CARDÁPIO ---
async function loadMenu() {
  if (!adminToken) return;
  try {
    const res = await fetch('/api/admin/cardapio', {
      headers: { 'Authorization': adminToken }
    });
    if (!res.ok) throw new Error();
    
    menuItems = await res.json();
    renderMenuGrid();
  } catch (err) {
    showToast('Falha ao carregar itens do cardápio.', 'error');
  }
}

function renderMenuGrid() {
  const container = document.getElementById('menu-items-admin');
  if (menuItems.length === 0) {
    container.innerHTML = `<div class="cart-empty glass" style="grid-column: 1/-1;"><p>Seu cardápio está vazio. Adicione pratos para começar!</p></div>`;
    return;
  }

  container.innerHTML = menuItems.map(item => {
    const hasDiscount = item.desconto_ativo && item.preco_desconto > 0;
    const priceDisplay = hasDiscount
      ? `<span class="price-original slashed">${formatCurrency(item.preco)}</span>
         <span class="price-discounted">${formatCurrency(item.preco_desconto)}</span>`
      : `<span class="price-original">${formatCurrency(item.preco)}</span>`;

    const statusBadge = item.disponivel 
      ? `<div class="menu-item-badge" style="background:var(--success)">ATIVO</div>` 
      : `<div class="menu-item-badge" style="background:var(--bg-tertiary); color:var(--text-muted)">PAUSADO</div>`;

    const imgUrl = item.imagem_url || 'https://images.unsplash.com/photo-1546069901-ba9599a7e63c?w=500';

    return `
      <div class="menu-item-card glass">
        <div class="menu-item-img" style="background-image: url('${imgUrl}')">
          ${statusBadge}
        </div>
        <div class="menu-item-info">
          <h3 class="menu-item-name">${item.nome}</h3>
          <p class="menu-item-desc">${item.descricao || ''}</p>
          <div class="menu-item-price-row">
            ${priceDisplay}
          </div>
        </div>
        <div class="menu-item-actions">
          <button class="btn btn-secondary" style="flex:1; padding:0.5rem;" onclick="openItemModal(${item.id})">Editar</button>
          <button class="btn btn-outline" style="padding:0.5rem; color:var(--danger); border-color:var(--border-color);" onclick="deleteMenuItem(${item.id})">Excluir</button>
        </div>
      </div>
    `;
  }).join('');
}

function openItemModal(id = null) {
  editingItemId = id;
  const overlay = document.getElementById('menu-item-overlay');
  
  if (id) {
    // Editar
    document.getElementById('modal-action-title').innerText = 'Editar Item do Cardápio';
    const item = menuItems.find(i => i.id === id);
    if (item) {
      document.getElementById('item-nome').value = item.nome;
      document.getElementById('item-categoria').value = item.categoria;
      document.getElementById('item-preco').value = item.preco;
      document.getElementById('item-desconto-checkbox').checked = item.desconto_ativo;
      document.getElementById('item-preco-desconto').value = item.preco_desconto || '';
      document.getElementById('item-descricao').value = item.descricao || '';
      document.getElementById('item-imagem-url').value = item.imagem_url || '';
      document.getElementById('item-disponivel').checked = item.disponivel;
      
      toggleDiscountInput(document.getElementById('item-desconto-checkbox'));
    }
  } else {
    // Incluir
    document.getElementById('modal-action-title').innerText = 'Adicionar Prato';
    document.getElementById('item-form').reset();
    document.getElementById('item-disponivel').checked = true;
    toggleDiscountInput(document.getElementById('item-desconto-checkbox'));
  }
  
  overlay.classList.add('open');
}

function closeItemModal() {
  document.getElementById('menu-item-overlay').classList.remove('open');
}

function toggleDiscountInput(cb) {
  document.getElementById('desconto-input-wrapper').style.display = cb.checked ? 'block' : 'none';
}

async function handleSaveItem(e) {
  e.preventDefault();
  const nome = document.getElementById('item-nome').value.trim();
  const categoria = document.getElementById('item-categoria').value.trim();
  const preco = parseFloat(document.getElementById('item-preco').value) || 0.0;
  const descontoAtivo = document.getElementById('item-desconto-checkbox').checked;
  const precoDesconto = parseFloat(document.getElementById('item-preco-desconto').value) || 0.0;
  const descricao = document.getElementById('item-descricao').value.trim();
  const imagemUrl = document.getElementById('item-imagem-url').value.trim();
  const disponivel = document.getElementById('item-disponivel').checked;

  const payload = {
    nome,
    categoria,
    preco,
    desconto_ativo: descontoAtivo,
    preco_desconto: descontoAtivo ? precoDesconto : 0.0,
    descricao,
    imagem_url: imagemUrl,
    disponivel
  };

  const url = editingItemId ? `/api/admin/cardapio/${editingItemId}` : '/api/admin/cardapio';
  const method = editingItemId ? 'PUT' : 'POST';

  try {
    const res = await fetch(url, {
      method,
      headers: {
        'Content-Type': 'application/json',
        'Authorization': adminToken
      },
      body: JSON.stringify(payload)
    });

    if (!res.ok) throw new Error();
    
    showToast(editingItemId ? 'Prato atualizado com sucesso!' : 'Prato adicionado com sucesso!', 'success');
    closeItemModal();
    loadMenu();
  } catch (err) {
    showToast('Falha ao salvar dados do item no cardápio.', 'error');
  }
}

async function deleteMenuItem(id) {
  if (!confirm('Tem certeza que deseja excluir permanentemente este item do cardápio?')) return;
  try {
    const res = await fetch(`/api/admin/cardapio/${id}`, {
      method: 'DELETE',
      headers: { 'Authorization': adminToken }
    });
    if (!res.ok) throw new Error();
    
    showToast('Item removido com sucesso!', 'success');
    loadMenu();
  } catch (err) {
    showToast('Erro ao remover item.', 'error');
  }
}

// --- GESTÃO DE MÉTODOS DE PAGAMENTO ---
async function loadPaymentsConfig() {
  if (!adminToken) return;
  try {
    const res = await fetch('/api/admin/config', {
      headers: { 'Authorization': adminToken }
    });
    if (!res.ok) throw new Error();
    
    const config = await res.json();
    document.getElementById('config-pix-ativo').checked = config.pix_ativo;
    document.getElementById('config-pix-chave').value = config.pix_chave || '';
    document.getElementById('config-credito-ativo').checked = config.cartao_credito_ativo;
    document.getElementById('config-debito-ativo').checked = config.cartao_debito_ativo;
    document.getElementById('config-dinheiro-ativo').checked = config.dinheiro_ativo;
    
    togglePixInput(document.getElementById('config-pix-ativo'));
  } catch (err) {
    showToast('Erro ao carregar configurações de pagamento.', 'error');
  }
}

function togglePixInput(cb) {
  document.getElementById('pix-key-wrapper').style.display = cb.checked ? 'block' : 'none';
}

async function handleSavePayments(e) {
  e.preventDefault();
  const pix_ativo = document.getElementById('config-pix-ativo').checked;
  const pix_chave = document.getElementById('config-pix-chave').value.trim();
  const cartao_credito_ativo = document.getElementById('config-credito-ativo').checked;
  const cartao_debito_ativo = document.getElementById('config-debito-ativo').checked;
  const dinheiro_ativo = document.getElementById('config-dinheiro-ativo').checked;

  if (pix_ativo && !pix_chave) {
    showToast('Chave PIX obrigatória se o PIX estiver ativado.', 'error');
    return;
  }

  const payload = {
    pix_ativo,
    pix_chave,
    cartao_credito_ativo,
    cartao_debito_ativo,
    dinheiro_ativo
  };

  try {
    const res = await fetch('/api/admin/config', {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': adminToken
      },
      body: JSON.stringify(payload)
    });
    if (!res.ok) throw new Error();
    
    showToast('Métodos de pagamento atualizados!', 'success');
    loadPaymentsConfig();
  } catch (err) {
    showToast('Erro ao salvar as configurações de pagamento.', 'error');
  }
}

// Configura eventos gerais do painel
function setupEventListeners() {
  document.getElementById('login-form').addEventListener('submit', handleLogin);
  document.getElementById('nav-logout-btn').addEventListener('click', handleLogout);
  
  // Navegação
  document.getElementById('nav-pedidos').addEventListener('click', () => switchTab('pedidos'));
  document.getElementById('nav-cardapio').addEventListener('click', () => switchTab('cardapio'));
  document.getElementById('nav-pagamentos').addEventListener('click', () => switchTab('pagamentos'));
  document.getElementById('nav-usuarios').addEventListener('click', () => switchTab('usuarios'));
  document.getElementById('nav-configuracoes').addEventListener('click', () => switchTab('configuracoes'));
  
  // Cardápio
  document.getElementById('btn-add-item').addEventListener('click', () => openItemModal());
  document.getElementById('modal-close-btn').addEventListener('click', closeItemModal);
  document.getElementById('item-form').addEventListener('submit', handleSaveItem);
  document.getElementById('item-desconto-checkbox').addEventListener('change', (e) => toggleDiscountInput(e.target));
  
  // Pagamentos
  document.getElementById('config-pix-ativo').addEventListener('change', (e) => togglePixInput(e.target));
  document.getElementById('payments-form').addEventListener('submit', handleSavePayments);

  // Usuários
  document.getElementById('btn-add-user').addEventListener('click', openUserModal);
  document.getElementById('user-modal-close-btn').addEventListener('click', closeUserModal);
  document.getElementById('user-modal-cancel-btn').addEventListener('click', closeUserModal);
  document.getElementById('user-form').addEventListener('submit', handleSaveUser);

  // Configurações Gerais
  document.getElementById('general-config-form').addEventListener('submit', handleSaveGeneralConfig);
  document.getElementById('btn-check-dns').addEventListener('click', handleCheckDNS);
}

// --- GESTÃO DE CONFIGURAÇÕES GERAIS ---
async function loadGeneralConfig() {
  if (!adminToken) return;
  try {
    const res = await fetch('/api/admin/config', {
      headers: { 'Authorization': adminToken }
    });
    if (!res.ok) throw new Error();
    const config = await res.json();
    
    document.getElementById('config-rest-nome').value = config.nome || '';
    document.getElementById('config-rest-domain').value = config.domain || '';

    const mainDomain = config.main_domain || 'deliverysistema.com.br';
    const protocol = window.location.protocol;
    const port = window.location.port ? `:${window.location.port}` : '';
    
    let subdomainUrl = "";
    if (window.location.hostname === 'localhost' || window.location.hostname === '127.0.0.1') {
      subdomainUrl = `${protocol}//${window.location.hostname}${port}/menu?tenant=${config.slug}`;
    } else {
      subdomainUrl = `${protocol}//${config.slug}.${mainDomain}${port}/menu`;
    }
    
    const subdomainLink = document.getElementById('lbl-subdomain-link');
    subdomainLink.href = subdomainUrl;
    subdomainLink.innerText = subdomainUrl;
    document.getElementById('lbl-main-domain').innerText = mainDomain;

    // Oculta wrapper de status de DNS anterior ao carregar
    document.getElementById('dns-status-wrapper').style.display = 'none';
  } catch (err) {
    showToast('Erro ao carregar configurações gerais.', 'error');
  }
}

async function handleCheckDNS() {
  const domain = document.getElementById('config-rest-domain').value.trim();
  if (!domain) {
    showToast('Digite um domínio próprio para testar.', 'error');
    return;
  }

  const wrapper = document.getElementById('dns-status-wrapper');
  wrapper.style.display = 'block';
  wrapper.style.background = 'rgba(255,255,255,0.05)';
  wrapper.style.color = '#fff';
  wrapper.innerText = 'Consultando servidores DNS...';

  try {
    const res = await fetch(`/api/admin/config/check-dns?domain=${encodeURIComponent(domain)}`, {
      headers: { 'Authorization': adminToken }
    });
    if (!res.ok) throw new Error();
    const data = await res.json();

    if (data.configured) {
      wrapper.style.background = 'rgba(46, 213, 115, 0.1)';
      wrapper.style.color = '#2ed573';
      wrapper.innerHTML = `<strong>✓ Conectado com sucesso!</strong> Seu domínio está apontando corretamente para o nosso servidor e o SSL será ativado no primeiro acesso.`;
    } else {
      wrapper.style.background = 'rgba(255, 71, 87, 0.1)';
      wrapper.style.color = '#ff4757';
      wrapper.innerHTML = `<strong>⚠️ Apontamento pendente.</strong> O domínio não parece estar apontando para nosso servidor ainda. Verifique as configurações de CNAME/A na sua registradora de domínio.`;
    }
  } catch (err) {
    wrapper.style.background = 'rgba(255, 71, 87, 0.1)';
    wrapper.style.color = '#ff4757';
    wrapper.innerText = 'Falha ao consultar DNS. Tente novamente.';
  }
}

async function handleSaveGeneralConfig(e) {
  e.preventDefault();
  const nome = document.getElementById('config-rest-nome').value.trim();
  const domain = document.getElementById('config-rest-domain').value.trim();

  try {
    const res = await fetch('/api/admin/config', {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': adminToken
      },
      body: JSON.stringify({ nome, domain: domain || null })
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || 'Erro ao salvar configurações gerais.');

    showToast('Configurações gerais salvas com sucesso!', 'success');
    localStorage.setItem('admin_name', nome);
    document.getElementById('logged-user-name').innerText = nome;
    loadGeneralConfig();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

// --- GESTÃO DE USUÁRIOS ---
let users = [];

async function loadUsers() {
  if (!adminToken) return;
  try {
    const res = await fetch('/api/admin/usuarios', {
      headers: { 'Authorization': adminToken }
    });
    if (!res.ok) throw new Error();
    users = await res.json();
    renderUsers();
  } catch (err) {
    showToast('Erro ao carregar usuários.', 'error');
  }
}

function renderUsers() {
  const container = document.getElementById('users-list-table');
  if (users.length === 0) {
    container.innerHTML = `<tr><td colspan="5" style="text-align:center; padding: 2rem;">Nenhum usuário cadastrado.</td></tr>`;
    return;
  }

  container.innerHTML = users.map(user => {
    const statusText = user.ativo ? '<span style="color:var(--success);">✓ Ativo</span>' : '<span style="color:var(--text-muted);">Inativo</span>';
    const isOwner = user.role === 'owner';
    const roleLabel = {
      'owner': 'Proprietário (Owner)',
      'admin': 'Administrador',
      'gerente': 'Gerente',
      'funcionario': 'Funcionário'
    }[user.role] || user.role;

    const deleteBtn = isOwner 
      ? `<span style="font-size:0.8rem; color:var(--text-muted); font-style:italic;">Sistema</span>` 
      : `<button class="btn btn-outline" style="padding: 0.25rem 0.5rem; font-size: 0.8rem; border-color: rgba(255, 71, 87, 0.3); color: #ff4757;" onclick="deleteUser(${user.id})">Excluir</button>`;

    return `
      <tr style="border-bottom: 1px solid var(--border-color);">
        <td style="padding: 0.75rem 0.5rem; font-weight:600; color:#fff;">${user.nome}</td>
        <td style="padding: 0.75rem 0.5rem;">${user.email}</td>
        <td style="padding: 0.75rem 0.5rem;"><span class="badge ${isOwner ? 'badge-primary' : 'badge-secondary'}" style="font-size:0.75rem; text-transform:uppercase;">${roleLabel}</span></td>
        <td style="padding: 0.75rem 0.5rem;">${statusText}</td>
        <td style="padding: 0.75rem 0.5rem; text-align: right;">${deleteBtn}</td>
      </tr>
    `;
  }).join('');
}

function openUserModal() {
  document.getElementById('user-form').reset();
  document.getElementById('user-overlay').classList.add('open');
}

function closeUserModal() {
  document.getElementById('user-overlay').classList.remove('open');
}

async function handleSaveUser(e) {
  e.preventDefault();
  const nome = document.getElementById('user-nome').value.trim();
  const email = document.getElementById('user-email').value.trim();
  const password = document.getElementById('user-password').value;
  const role = document.getElementById('user-role').value;

  try {
    const res = await fetch('/api/admin/usuarios', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': adminToken
      },
      body: JSON.stringify({ nome, email, password, role })
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || 'Erro ao cadastrar usuário.');

    showToast('Usuário cadastrado com sucesso!', 'success');
    closeUserModal();
    loadUsers();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

async function deleteUser(id) {
  if (!confirm('Deseja realmente remover este usuário do painel?')) return;
  try {
    const res = await fetch(`/api/admin/usuarios/${id}`, {
      method: 'DELETE',
      headers: { 'Authorization': adminToken }
    });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || 'Erro ao remover usuário.');

    showToast('Usuário removido com sucesso!', 'success');
    loadUsers();
  } catch (err) {
    showToast(err.message, 'error');
  }
}
