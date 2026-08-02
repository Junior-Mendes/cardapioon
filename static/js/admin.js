// Painel administrativo do restaurante.
//
// Depende de common.js (esc, api, Sessao, formatCurrency, showToast), que tem de ser
// carregado antes deste ficheiro.
//
// Alterações relevantes em relação à versão anterior:
//   - Autenticação por JWT com renovação automática, em vez do token de formato
//     "admin_user_<tenant>_<user>", que qualquer pessoa podia escrever.
//   - Todo o dado vindo do servidor é escapado com esc() antes de entrar em innerHTML.
//     Sem isso, um produto chamado "<img onerror=...>" executava no painel do lojista.
//   - Handlers ligados por addEventListener em vez de onclick inline, porque a CSP não
//     permite 'unsafe-inline' em script-src.
//   - Pix removido (não existe em Portugal). O MVP é levantamento ao balcão com pagamento
//     na caixa: dinheiro ou cartão no terminal, sem pagamento na aplicação.
//   - Domínio personalizado passa a exigir verificação de propriedade por registo TXT.

'use strict';

let currentTab = 'pedidos';
let menuItems = [];
let orders = [];
let users = [];
let editingItemId = null;
// Taxa de IVA sugerida pelo restaurante para produtos novos, em pontos base.
let taxaIVAOmissao = 1300;
let timerPedidos = null;

document.addEventListener('DOMContentLoaded', () => {
  checkAuth();
  setupEventListeners();
});

// --- Sessão ---

function checkAuth() {
  const overlay = document.getElementById('login-overlay');

  if (!Sessao.temSessao()) {
    overlay.classList.add('open');
    if (timerPedidos) {
      clearInterval(timerPedidos);
      timerPedidos = null;
    }
    return;
  }

  overlay.classList.remove('open');
  document.getElementById('logged-user-name').innerText = Sessao.info().nome || 'Lojista';

  loadDashboardData();

  // Registo acabado de fazer: abre as Configurações, onde o estado do endereço é
  // mostrado, em vez de deixar o lojista num quadro de encomendas vazio sem saber se
  // a loja já está no ar.
  if (new URLSearchParams(window.location.search).get('novo') === '1') {
    switchTab('configuracoes');
    showToast('Bem-vindo! Estamos a preparar o seu endereço.', 'info');
    // Limpa o parâmetro para que um refresh não repita a mensagem.
    window.history.replaceState({}, '', '/admin');
  }

  // Actualização periódica das encomendas.
  //
  // Continua a ser polling, mas agora sobre uma lista paginada em vez de todas as
  // encomendas de sempre. A substituição por SSE está planeada para a Fase 2.
  if (!timerPedidos) {
    timerPedidos = setInterval(loadOrders, 15000);
  }
}

async function handleLogin(e) {
  e.preventDefault();
  const identifier = document.getElementById('login-slug').value.trim();
  const password = document.getElementById('login-password').value;

  try {
    const dados = await api('/api/tenant/login', {
      metodo: 'POST',
      corpo: { identifier, password },
      autenticado: false,
    });

    Sessao.guardar(dados);
    showToast(`Bem-vindo, ${dados.usuario.nome}!`, 'success');
    checkAuth();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

async function handleLogout() {
  const refresh = Sessao.refreshToken();
  if (refresh) {
    // Revoga o refresh token no servidor; se falhar, a sessão local é limpa de qualquer forma.
    try {
      await api('/api/tenant/logout', {
        metodo: 'POST',
        corpo: { refresh_token: refresh },
        autenticado: false,
      });
    } catch {
      /* ignorado de propósito */
    }
  }
  pararVigilanciaStorefront();
  Sessao.limpar();
  window.location.reload();
}

async function handleEsqueciSenha() {
  const email = prompt('Indique o email da sua conta:');
  if (!email) return;

  try {
    const dados = await api('/api/tenant/esqueci-senha', {
      metodo: 'POST',
      corpo: { email: email.trim() },
      autenticado: false,
    });
    showToast(dados.message, 'success');
  } catch (err) {
    showToast(err.message, 'error');
  }
}

// --- Navegação ---

function loadDashboardData() {
  loadOrders();
  loadMenu();
  loadPaymentsConfig();
}

function switchTab(tab) {
  currentTab = tab;

  ['pedidos', 'cardapio', 'pagamentos', 'usuarios', 'configuracoes'].forEach((nome) => {
    const painel = document.getElementById(`tab-${nome}`);
    if (painel) painel.style.display = tab === nome ? 'block' : 'none';

    const nav = document.getElementById(`nav-${nome}`);
    if (nav) nav.classList.toggle('active', tab === nome);
  });

  if (tab !== 'configuracoes') pararVigilanciaStorefront();

  if (tab === 'usuarios') loadUsers();
  else if (tab === 'configuracoes') loadGeneralConfig();
}

// --- Encomendas ---

async function loadOrders() {
  if (!Sessao.temSessao()) return;

  try {
    // A resposta é paginada: { encomendas: [...], paginacao: {...} }
    const dados = await api('/api/admin/pedidos?por_pagina=100');
    orders = dados.encomendas || [];
    renderOrdersBoard();
    renderMetrics();
  } catch (err) {
    if (err.status === 401) {
      checkAuth();
      return;
    }
    showToast('Não foi possível sincronizar as encomendas.', 'error');
  }
}

function renderMetrics() {
  // Soma em cêntimos: inteiros, exacta.
  const vendasCents = orders
    .filter((o) => o.status === 'finalizado')
    .reduce((soma, o) => soma + Number(o.valor_total_cents || 0), 0);
  const activas = orders.filter(
    (o) => o.status !== 'finalizado' && o.status !== 'cancelado'
  ).length;

  document.getElementById('metric-sales').innerText = formatCents(vendasCents);
  document.getElementById('metric-orders').innerText = activas;
  document.getElementById('metric-total-orders').innerText = orders.length;
}

// Etiquetas de pagamento. No MVP tudo é pago na caixa ao levantar; os valores antigos
// continuam mapeados para que o histórico de encomendas permaneça legível.
const ETIQUETAS_PAGAMENTO = {
  dinheiro: 'Dinheiro na caixa',
  cartao: 'Cartão na caixa',
  retirada_dinheiro: 'Dinheiro na caixa',
  retirada_cartao: 'Cartão na caixa',
  cartao_credito: 'Cartão online (histórico)',
  pix: 'Pix (histórico)',
  tpa: 'Cartão na caixa',
};

function renderOrdersBoard() {
  const colunas = {
    pendente: document.getElementById('list-pendente'),
    preparando: document.getElementById('list-preparando'),
    pronto: document.getElementById('list-pronto'),
    finalizado: document.getElementById('list-finalizado'),
  };
  Object.values(colunas).forEach((c) => {
    if (c) c.innerHTML = '';
  });

  const contagens = { pendente: 0, preparando: 0, pronto: 0, finalizado: 0 };

  orders.forEach((order) => {
    const nomeColuna = order.status === 'cancelado' ? 'finalizado' : order.status;
    const coluna = colunas[nomeColuna];
    if (!coluna) return;
    contagens[nomeColuna]++;

    const card = document.createElement('div');
    card.className = 'order-card glass';
    card.style.borderLeftColor = corDoEstado(order.status);

    const linhasItens = (order.itens || [])
      .map(
        (i) => `
      <div class="order-item-row">
        <span>${esc(i.quantidade)}x ${esc(i.nome)}</span>
        <span>${esc(formatCents(i.total_linha_cents ?? i.preco_unitario_cents * i.quantidade))}</span>
      </div>`
      )
      .join('');

    const etiquetaPagamento =
      ETIQUETAS_PAGAMENTO[order.forma_pagamento] || order.forma_pagamento;

    const badgeCancelado =
      order.status === 'cancelado'
        ? '<span style="color:var(--danger); font-weight:700;">[RECUSADA / CANCELADA]</span>'
        : '';

    card.innerHTML = `
      <div class="order-header">
        <span class="order-id">Encomenda #${esc(order.id)}</span>
        <span class="order-time">${esc(formatTime(order.created_at))}</span>
      </div>
      <div class="order-client">
        ${esc(order.cliente_nome)}
        <div class="order-phone">${esc(order.cliente_telefone)}</div>
      </div>
      <div class="order-items">${linhasItens}</div>
      <div class="order-footer">
        <span class="order-price">${esc(formatCents(order.valor_total_cents))}</span>
        <span class="order-payment">${esc(etiquetaPagamento)}</span>
      </div>
      ${badgeCancelado}
      ${botoesDeAccao(order)}
    `;

    card.querySelectorAll('[data-accao]').forEach((btn) => {
      btn.addEventListener('click', () =>
        updateOrderStatus(Number(btn.dataset.pedido), btn.dataset.accao)
      );
    });

    coluna.appendChild(card);
  });

  Object.keys(contagens).forEach((k) => {
    const el = document.getElementById(`count-${k}`);
    if (el) el.innerText = contagens[k];
  });
}

function corDoEstado(estado) {
  switch (estado) {
    case 'cancelado':
      return 'var(--danger)';
    case 'pronto':
      return 'var(--success)';
    case 'preparando':
      return 'var(--warning)';
    default:
      return 'var(--primary)';
  }
}

function botoesDeAccao(order) {
  const id = esc(order.id);
  switch (order.status) {
    case 'pendente':
      return `
        <div class="order-actions">
          <button class="btn btn-primary order-btn" data-pedido="${id}" data-accao="preparando">Aceitar e preparar</button>
          <button class="btn btn-secondary order-btn" data-pedido="${id}" data-accao="cancelado" style="color:var(--danger)">Recusar</button>
        </div>`;
    case 'preparando':
      return `
        <div class="order-actions">
          <button class="btn btn-primary order-btn" data-pedido="${id}" data-accao="pronto" style="background:var(--success)">Pronta</button>
        </div>`;
    case 'pronto':
      return `
        <div class="order-actions">
          <button class="btn btn-primary order-btn" data-pedido="${id}" data-accao="finalizado" style="background:var(--info)">Entregue / levantada</button>
        </div>`;
    default:
      return '';
  }
}

async function updateOrderStatus(id, novoEstado) {
  try {
    await api(`/api/admin/pedidos/${id}/status`, {
      metodo: 'PUT',
      corpo: { status: novoEstado },
    });
    showToast('Estado actualizado.', 'success');
    loadOrders();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

// --- Menu ---

async function loadMenu() {
  if (!Sessao.temSessao()) return;
  try {
    menuItems = (await api('/api/admin/cardapio')) || [];
    renderMenuGrid();
  } catch (err) {
    if (err.status === 403) return; // funcionário sem permissão para o menu
    showToast('Não foi possível carregar o menu.', 'error');
  }
}

const IMAGEM_OMISSAO = 'https://images.unsplash.com/photo-1546069901-ba9599a7e63c?w=500';

function renderMenuGrid() {
  const container = document.getElementById('menu-items-admin');
  if (!container) return;

  if (menuItems.length === 0) {
    container.innerHTML =
      '<div class="cart-empty glass" style="grid-column: 1/-1;"><p>O seu menu está vazio. Adicione pratos para começar.</p></div>';
    return;
  }

  container.innerHTML = menuItems
    .map((item) => {
      const temDesconto = item.desconto_ativo && Number(item.preco_desconto_cents) > 0;
      const precos = temDesconto
        ? `<span class="price-original slashed">${esc(formatCents(item.preco_cents))}</span>
           <span class="price-discounted">${esc(formatCents(item.preco_desconto_cents))}</span>`
        : `<span class="price-original">${esc(formatCents(item.preco_cents))}</span>`;

      const badge = item.disponivel
        ? '<div class="menu-item-badge" style="background:var(--success)">ACTIVO</div>'
        : '<div class="menu-item-badge" style="background:var(--bg-tertiary); color:var(--text-muted)">EM PAUSA</div>';

      // O URL da imagem entra numa propriedade CSS: escAttr remove parênteses, que de
      // outro modo fechariam o url(...) e permitiriam injectar mais CSS.
      const imagem = escAttr(item.imagem_url || IMAGEM_OMISSAO);

      return `
      <div class="menu-item-card glass">
        <div class="menu-item-img" style="background-image: url('${imagem}')">${badge}</div>
        <div class="menu-item-info">
          <h3 class="menu-item-name">${esc(item.nome)}</h3>
          <p class="menu-item-desc">${esc(item.descricao || '')}</p>
          <div class="menu-item-price-row">${precos}</div>
        </div>
        <div class="menu-item-actions">
          <button class="btn btn-secondary" style="flex:1; padding:0.5rem;" data-editar="${esc(item.id)}">Editar</button>
          <button class="btn btn-outline" style="padding:0.5rem; color:var(--danger); border-color:var(--border-color);" data-apagar="${esc(item.id)}">Eliminar</button>
        </div>
      </div>`;
    })
    .join('');

  container.querySelectorAll('[data-editar]').forEach((b) => {
    b.addEventListener('click', () => openItemModal(Number(b.dataset.editar)));
  });
  container.querySelectorAll('[data-apagar]').forEach((b) => {
    b.addEventListener('click', () => deleteMenuItem(Number(b.dataset.apagar)));
  });
}

function openItemModal(id = null) {
  editingItemId = id;
  const overlay = document.getElementById('menu-item-overlay');

  if (id) {
    document.getElementById('modal-action-title').innerText = 'Editar produto';
    const item = menuItems.find((i) => i.id === id);
    if (item) {
      document.getElementById('item-nome').value = item.nome;
      document.getElementById('item-categoria').value = item.categoria;
      document.getElementById('item-preco').value = (item.preco_cents / 100).toFixed(2);
      document.getElementById('item-desconto-checkbox').checked = item.desconto_ativo;
      document.getElementById('item-preco-desconto').value =
        item.preco_desconto_cents ? (item.preco_desconto_cents / 100).toFixed(2) : '';
      SegGroup.definir('item-taxa-iva', item.taxa_iva_bp ?? 1300);
      document.getElementById('item-descricao').value = item.descricao || '';
      document.getElementById('item-imagem-url').value = item.imagem_url || '';
      mostrarPreview(document.getElementById('preview-item-imagem'), item.imagem_url || '');
      document.getElementById('item-disponivel').checked = item.disponivel;
      toggleDiscountInput(document.getElementById('item-desconto-checkbox'));
    }
  } else {
    document.getElementById('modal-action-title').innerText = 'Adicionar produto';
    document.getElementById('item-form').reset();
    // Produto novo herda a taxa sugerida do restaurante, que o lojista pode alterar.
    SegGroup.definir('item-taxa-iva', taxaIVAOmissao);
    document.getElementById('item-imagem-url').value = '';
    mostrarPreview(document.getElementById('preview-item-imagem'), '');
    document.getElementById('item-disponivel').checked = true;
    toggleDiscountInput(document.getElementById('item-desconto-checkbox'));
  }

  actualizarDetalheIVA();
  overlay.classList.add('open');
}

function closeItemModal() {
  document.getElementById('menu-item-overlay').classList.remove('open');
}

function toggleDiscountInput(cb) {
  const wrapper = document.getElementById('desconto-input-wrapper');
  if (wrapper) wrapper.style.display = cb && cb.checked ? 'block' : 'none';
}

async function handleSaveItem(e) {
  e.preventDefault();

  const descontoAtivo = document.getElementById('item-desconto-checkbox').checked;
  const preco = parseFloat(document.getElementById('item-preco').value) || 0;
  const precoDesconto = parseFloat(document.getElementById('item-preco-desconto').value) || 0;

  // Validação no cliente apenas para dar uma mensagem imediata; o servidor valida o mesmo,
  // porque esta pode ser contornada.
  if (preco <= 0) {
    showToast('Indique um preço maior que zero.', 'error');
    return;
  }
  if (descontoAtivo && (precoDesconto <= 0 || precoDesconto >= preco)) {
    showToast('O preço com desconto tem de ser inferior ao preço normal.', 'error');
    return;
  }

  const payload = {
    nome: document.getElementById('item-nome').value.trim(),
    categoria: document.getElementById('item-categoria').value.trim(),
    // Enviado como texto: o servidor converte directamente para cêntimos, sem passar
    // por float em nenhum ponto.
    preco_texto: document.getElementById('item-preco').value.trim(),
    preco_desconto_texto: descontoAtivo
      ? document.getElementById('item-preco-desconto').value.trim()
      : '',
    taxa_iva_bp: Number(SegGroup.valor('item-taxa-iva')) || 0,
    desconto_ativo: descontoAtivo,
    descricao: document.getElementById('item-descricao').value.trim(),
    imagem_url: document.getElementById('item-imagem-url').value.trim(),
    disponivel: document.getElementById('item-disponivel').checked,
  };

  try {
    await api(
      editingItemId ? `/api/admin/cardapio/${editingItemId}` : '/api/admin/cardapio',
      { metodo: editingItemId ? 'PUT' : 'POST', corpo: payload }
    );
    showToast(editingItemId ? 'Produto actualizado.' : 'Produto adicionado.', 'success');
    closeItemModal();
    loadMenu();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

async function deleteMenuItem(id) {
  if (!confirm('Eliminar definitivamente este produto do menu?')) return;
  try {
    await api(`/api/admin/cardapio/${id}`, { metodo: 'DELETE' });
    showToast('Produto eliminado.', 'success');
    loadMenu();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

// actualizarDetalheIVA mostra a decomposição do preço introduzido.
//
// O lojista escreve o preço final, que é o que a lei manda afixar. Mostrar aqui quanto
// desse valor é imposto evita a dúvida mais comum: "o preço que escrevi já inclui IVA?".
function actualizarDetalheIVA() {
  const alvo = document.getElementById('item-iva-detalhe');
  if (!alvo) return;

  const descontoAtivo = document.getElementById('item-desconto-checkbox')?.checked;
  const campoPreco = descontoAtivo ? 'item-preco-desconto' : 'item-preco';
  const cents = parseValor(valorDe(campoPreco));
  const taxa = Number(SegGroup.valor('item-taxa-iva')) || 0;

  if (cents === null || cents <= 0) {
    alvo.innerHTML = 'Indique um preço para ver a decomposição.';
    return;
  }

  const iva = ivaIncluido(cents, taxa);
  const base = cents - iva;
  const etiquetaTaxa = taxa > 0 ? `${(taxa / 100).toString().replace('.', ',')}%` : 'isento';

  alvo.innerHTML = `
    <strong>${esc(formatCents(cents))}</strong> é o que o cliente paga${descontoAtivo ? ' (preço com desconto)' : ''}.<br>
    Base sem IVA: ${esc(formatCents(base))} &nbsp;·&nbsp; IVA (${esc(etiquetaTaxa)}): ${esc(formatCents(iva))}
  `;
}

// --- Métodos de pagamento ---

async function loadPaymentsConfig() {
  if (!Sessao.temSessao()) return;
  try {
    const config = await api('/api/admin/config');
    definirCheck('config-cartao-ativo', config.cartao_ativo);
    definirCheck('config-dinheiro-ativo', config.dinheiro_ativo);
  } catch (err) {
    if (err.status === 403) return;
    showToast('Não foi possível carregar os métodos de pagamento.', 'error');
  }
}

function definirCheck(id, valor) {
  const el = document.getElementById(id);
  if (el) el.checked = Boolean(valor);
}

function lerCheck(id) {
  const el = document.getElementById(id);
  return el ? el.checked : false;
}

async function handleSavePayments(e) {
  e.preventDefault();

  const payload = {
    dinheiro_ativo: lerCheck('config-dinheiro-ativo'),
    cartao_ativo: lerCheck('config-cartao-ativo'),
  };

  if (!payload.dinheiro_ativo && !payload.cartao_ativo) {
    showToast('Active pelo menos um método de pagamento, ou não poderá receber encomendas.', 'error');
    return;
  }

  try {
    await api('/api/admin/config', { metodo: 'PUT', corpo: payload });
    showToast('Métodos de pagamento actualizados.', 'success');
    loadPaymentsConfig();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

// --- Configurações gerais e domínio ---

async function loadGeneralConfig() {
  if (!Sessao.temSessao()) return;
  try {
    const config = await api('/api/admin/config');

    definirValor('config-rest-nome', config.nome || '');
    definirValor('config-rest-nif', config.nif || '');
    taxaIVAOmissao = config.taxa_iva_omissao_bp ?? 1300;
    SegGroup.definir('config-taxa-iva-omissao', taxaIVAOmissao);
    definirValor('config-rest-domain', config.domain || '');

    // Identidade visual
    definirValor('config-logo-url', config.logo_url || '');
    mostrarPreview(document.getElementById('preview-logo'), config.logo_url || '');
    definirValor('config-descricao-curta', config.descricao_curta || '');
    definirCheck('config-mostrar-marca', config.mostrar_marca_plataforma !== false);
    definirCorPrimaria(config.cor_primaria || '');

    const link = document.getElementById('lbl-subdomain-link');
    if (link) {
      const url = config.storefront_url || '#';
      link.href = url;
      link.innerText = url;
    }
    const lblDominio = document.getElementById('lbl-main-domain');
    if (lblDominio) lblDominio.innerText = config.main_domain || '';

    mostrarEstadoDominio(config);

    // O link do subdomínio só é revelado quando o endereço responde de facto.
    tentativasStorefront = 0;
    vigiarStorefront();
  } catch (err) {
    if (err.status === 403) return;
    showToast('Não foi possível carregar as configurações.', 'error');
  }
}

function definirValor(id, valor) {
  const el = document.getElementById(id);
  if (el) el.value = valor;
}

// mostrarEstadoDominio comunica em que ponto do fluxo de verificação o domínio está.
function mostrarEstadoDominio(config) {
  const wrapper = document.getElementById('dns-status-wrapper');
  if (!wrapper) return;

  if (!config.domain) {
    wrapper.style.display = 'none';
    return;
  }

  wrapper.style.display = 'block';

  if (config.domain_status === 'verified') {
    wrapper.style.background = 'rgba(46, 213, 115, 0.1)';
    wrapper.style.color = '#2ed573';
    wrapper.innerHTML = `<strong>✓ Domínio verificado.</strong> ${esc(config.domain)} está activo; o certificado é emitido no primeiro acesso.`;
    return;
  }

  wrapper.style.background = 'rgba(255, 165, 2, 0.1)';
  wrapper.style.color = '#ffa502';
  wrapper.innerHTML =
    '<strong>Verificação pendente.</strong> Guarde o domínio para obter o registo TXT e depois clique em verificar.';
}

// handleSaveGeneralConfig grava o nome e o NIF; o domínio segue o seu próprio fluxo.
async function handleSaveGeneralConfig(e) {
  e.preventDefault();

  const nome = document.getElementById('config-rest-nome').value.trim();
  const nifEl = document.getElementById('config-rest-nif');

  try {
    const payload = { nome };
    if (nifEl) payload.nif = nifEl.value.trim();

    // Identidade visual. Enviados sempre juntos, para que limpar um campo o limpe de facto
    // (o servidor distingue ausente de vazio).
    payload.logo_url = valorDe('config-logo-url');
    payload.cor_primaria = valorDe('config-cor-primaria-hex');
    payload.descricao_curta = valorDe('config-descricao-curta');
    payload.mostrar_marca_plataforma = lerCheck('config-mostrar-marca');
    payload.taxa_iva_omissao_bp = Number(SegGroup.valor('config-taxa-iva-omissao')) || 0;

    await api('/api/admin/config', { metodo: 'PUT', corpo: payload });
    showToast('Configurações guardadas.', 'success');
    loadGeneralConfig();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

// handleSaveDomain inicia a verificação de propriedade e mostra o registo TXT a criar.
async function handleSaveDomain() {
  const domain = document.getElementById('config-rest-domain').value.trim();

  try {
    const dados = await api('/api/admin/config/dominio', {
      metodo: 'POST',
      corpo: { domain },
    });

    if (!dados.registo_txt) {
      showToast(dados.message || 'Domínio removido.', 'success');
      loadGeneralConfig();
      return;
    }

    const wrapper = document.getElementById('dns-status-wrapper');
    wrapper.style.display = 'block';
    wrapper.style.background = 'rgba(255, 165, 2, 0.1)';
    wrapper.style.color = '#ffa502';
    wrapper.innerHTML = `
      <strong>Falta um passo: provar que o domínio é seu.</strong>
      <p style="margin:0.5rem 0;">Crie este registo no seu registrador de domínios:</p>
      <table style="width:100%; font-size:0.85rem; margin-bottom:0.75rem;">
        <tr><td style="padding:2px 8px 2px 0;">Tipo</td><td><code>${esc(dados.registo_txt.tipo)}</code></td></tr>
        <tr><td style="padding:2px 8px 2px 0;">Nome</td><td><code>${esc(dados.registo_txt.nome)}</code></td></tr>
        <tr><td style="padding:2px 8px 2px 0;">Valor</td><td><code>${esc(dados.registo_txt.valor)}</code></td></tr>
      </table>
      <p style="margin:0.5rem 0;">E este, para encaminhar o tráfego:</p>
      <table style="width:100%; font-size:0.85rem;">
        <tr><td style="padding:2px 8px 2px 0;">Tipo</td><td><code>${esc(dados.registo_encaminhamento.tipo)}</code></td></tr>
        <tr><td style="padding:2px 8px 2px 0;">Nome</td><td><code>${esc(dados.registo_encaminhamento.nome)}</code></td></tr>
        <tr><td style="padding:2px 8px 2px 0;">Valor</td><td><code>${esc(dados.registo_encaminhamento.valor)}</code></td></tr>
      </table>
      <p style="margin-top:0.75rem; font-size:0.8rem;">${esc(dados.registo_encaminhamento.nota || '')}</p>
    `;
    showToast('Crie o registo TXT e depois clique em verificar.', 'info');
  } catch (err) {
    showToast(err.message, 'error');
  }
}

async function handleVerifyDomain() {
  const wrapper = document.getElementById('dns-status-wrapper');
  wrapper.style.display = 'block';
  wrapper.style.background = 'rgba(255,255,255,0.05)';
  wrapper.style.color = '#fff';
  wrapper.innerText = 'A consultar os servidores DNS...';

  try {
    const dados = await api('/api/admin/config/dominio/verificar', { metodo: 'POST' });

    if (dados.verificado) {
      wrapper.style.background = 'rgba(46, 213, 115, 0.1)';
      wrapper.style.color = '#2ed573';
      wrapper.innerHTML = `<strong>✓ Domínio verificado.</strong> ${esc(dados.message)}`;
      loadGeneralConfig();
    } else {
      wrapper.style.background = 'rgba(255, 165, 2, 0.1)';
      wrapper.style.color = '#ffa502';
      wrapper.innerHTML = `<strong>Ainda não.</strong> ${esc(dados.motivo)}`;
    }
  } catch (err) {
    wrapper.style.background = 'rgba(255, 71, 87, 0.1)';
    wrapper.style.color = '#ff4757';
    wrapper.innerText = err.message;
  }
}

// --- Upload de imagens ---

/**
 * carregarImagem envia um ficheiro escolhido pelo utilizador e devolve o URL público.
 *
 * O ficheiro é processado no servidor: redimensionado, recodificado (o que remove os
 * metadados EXIF, incluindo as coordenadas GPS que as fotos de telemóvel trazem) e gravado
 * com nome aleatório.
 *
 * @param {File} ficheiro
 * @param {'produto'|'logo'} uso
 * @returns {Promise<string>} o caminho público da imagem
 */
async function carregarImagem(ficheiro, uso) {
  const MAX_BYTES = 8 * 1024 * 1024;
  if (ficheiro.size > MAX_BYTES) {
    throw new Error('A imagem tem mais de 8 MB. Escolha uma imagem menor.');
  }

  const formulario = new FormData();
  formulario.append('ficheiro', ficheiro);

  // fetch directo em vez de api(): api() envia JSON, e um upload precisa de multipart
  // com a fronteira definida pelo browser. O cabeçalho Content-Type é deixado de fora
  // de propósito — defini-lo à mão parte a fronteira.
  const enviar = async () => fetch(`/api/admin/upload?uso=${encodeURIComponent(uso)}`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${Sessao.accessToken()}` },
    body: formulario,
  });

  let res = await enviar();

  // Um upload pode demorar e apanhar o access token a expirar a meio.
  if (res.status === 401 && Sessao.refreshToken()) {
    const novo = await renovarSessaoSeNecessario();
    if (novo) res = await enviar();
  }

  const dados = await res.json().catch(() => null);
  if (!res.ok) {
    throw new Error(dados?.error || 'Não foi possível carregar a imagem.');
  }
  return dados.url;
}

// renovarSessaoSeNecessario reaproveita o fluxo de renovação de common.js através de uma
// chamada autenticada trivial.
async function renovarSessaoSeNecessario() {
  try {
    await api('/api/admin/config');
    return true;
  } catch {
    return false;
  }
}

/**
 * ligarSelectorDeImagem liga um trio (botão, input de ficheiro, pré-visualização).
 *
 * Mostra a pré-visualização local imediatamente, antes do upload terminar: numa ligação
 * móvel lenta, esperar pelo servidor para ver a imagem escolhida parece que nada aconteceu.
 */
function ligarSelectorDeImagem({ botaoId, inputId, previewId, urlId, estadoId, removerId, uso }) {
  const botao = document.getElementById(botaoId);
  const input = document.getElementById(inputId);
  const preview = document.getElementById(previewId);
  const campoUrl = document.getElementById(urlId);
  const estado = document.getElementById(estadoId);
  const remover = document.getElementById(removerId);

  if (!botao || !input || !preview || !campoUrl) return;

  botao.addEventListener('click', () => input.click());

  input.addEventListener('change', async () => {
    const ficheiro = input.files && input.files[0];
    if (!ficheiro) return;

    // Pré-visualização local imediata.
    const urlLocal = URL.createObjectURL(ficheiro);
    mostrarPreview(preview, urlLocal);

    const textoOriginal = estado ? estado.textContent : '';
    if (estado) estado.textContent = 'A carregar…';
    botao.disabled = true;

    try {
      const url = await carregarImagem(ficheiro, uso);
      campoUrl.value = url;
      // Passa a apontar para o ficheiro no servidor, já processado.
      mostrarPreview(preview, url);
      if (estado) estado.textContent = 'Imagem carregada. Guarde para aplicar.';
      showToast('Imagem carregada.', 'success');
    } catch (err) {
      // Reverte a pré-visualização para o que estava gravado.
      mostrarPreview(preview, campoUrl.value);
      if (estado) estado.textContent = textoOriginal;
      showToast(err.message, 'error');
    } finally {
      botao.disabled = false;
      URL.revokeObjectURL(urlLocal);
      // Permite escolher o mesmo ficheiro outra vez depois de um erro.
      input.value = '';
    }
  });

  if (remover) {
    remover.addEventListener('click', () => {
      campoUrl.value = '';
      mostrarPreview(preview, '');
      if (estado) estado.textContent = 'Imagem removida. Guarde para aplicar.';
    });
  }
}

// mostrarPreview pinta a miniatura, ou o texto de vazio quando não há imagem.
function mostrarPreview(elemento, url) {
  if (!elemento) return;

  if (url) {
    elemento.style.backgroundImage = `url('${escAttr(url)}')`;
    elemento.textContent = '';
  } else {
    elemento.style.backgroundImage = '';
    elemento.innerHTML = elemento.dataset.vazio || 'sem<br>imagem';
  }
}

// --- Identidade visual ---

function valorDe(id) {
  const el = document.getElementById(id);
  return el ? el.value.trim() : '';
}

// definirCorPrimaria sincroniza o selector de cor, o campo hexadecimal e a pré-visualização.
function definirCorPrimaria(cor) {
  const seletor = document.getElementById('config-cor-primaria');
  const hex = document.getElementById('config-cor-primaria-hex');
  if (!seletor || !hex) return;

  if (cor) {
    seletor.value = cor;
    hex.value = cor;
  } else {
    hex.value = '';
  }
  actualizarPreviewCor();
}

// actualizarPreviewCor mostra ao lojista como fica o botão com a cor escolhida.
//
// Calcula o contraste da mesma forma que o servidor: uma cor de marca clara com texto
// branco por cima torna o botão de encomendar ilegível, e o lojista escolhe a cor sem
// pensar nisso.
function actualizarPreviewCor() {
  const preview = document.getElementById('preview-cor');
  const texto = document.getElementById('preview-cor-texto');
  const aviso = document.getElementById('preview-cor-aviso');
  if (!preview || !texto) return;

  const cor = valorDe('config-cor-primaria-hex') || '#e63946';
  const rgb = corParaRGB(cor);
  if (!rgb) return;

  const luminancia = luminanciaRelativa(rgb);
  const corTexto = luminancia > 0.45 ? '#111111' : '#ffffff';

  preview.style.background = cor;
  texto.style.color = corTexto;

  if (aviso) {
    aviso.style.color = corTexto;
    aviso.textContent = luminancia > 0.45
      ? 'Cor clara: usamos texto escuro por cima'
      : 'Cor escura: usamos texto claro por cima';
  }
}

function corParaRGB(hex) {
  const h = String(hex).replace('#', '');
  if (!/^[0-9a-fA-F]{6}$/.test(h)) return null;
  return [
    parseInt(h.slice(0, 2), 16),
    parseInt(h.slice(2, 4), 16),
    parseInt(h.slice(4, 6), 16),
  ];
}

// luminanciaRelativa segue a definição da WCAG, igual à do servidor.
function luminanciaRelativa([r, g, b]) {
  const canal = (v) => {
    const s = v / 255;
    return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * canal(r) + 0.7152 * canal(g) + 0.0722 * canal(b);
}

// --- Estado do endereço público ---

// Sondagem do estado do endereço, activa apenas enquanto ainda não está pronto.
let timerStorefront = null;
let tentativasStorefront = 0;

// Limite de sondagens: 2s × 45 ≈ 90 segundos. O certificado costuma ser emitido em
// menos de um minuto; passado esse tempo é mais honesto dizer que algo não está bem do
// que continuar a girar indefinidamente.
const MAX_TENTATIVAS_STOREFRONT = 45;

/**
 * vigiarStorefront mostra o estado do endereço público e liberta o link quando responder.
 *
 * Existe uma janela de alguns segundos entre criar o restaurante e o endereço funcionar: o
 * encaminhador tem de detectar a rota nova e o certificado tem de ser emitido. Dar ao
 * lojista um link que devolve erro nesse intervalo parece uma avaria do produto.
 *
 * A espera é mostrada aqui, no painel, e não no subdomínio: sem rota o pedido não chega à
 * aplicação, e sem certificado o browser falha no TLS antes de haver HTTP, pelo que não há
 * como servir uma página de espera nesse endereço.
 */
async function vigiarStorefront() {
  const caixa = document.getElementById('storefront-estado');
  const texto = document.getElementById('storefront-estado-texto');
  const link = document.getElementById('lbl-subdomain-link');
  if (!caixa || !link) return;

  let dados;
  try {
    dados = await api('/api/admin/storefront/status');
  } catch (err) {
    if (err.status === 403) return; // sem permissões para ver configurações
    // Uma falha de rede não deve esconder o link: é mais útil mostrá-lo do que
    // deixar o lojista sem nada.
    mostrarStorefrontPronto();
    return;
  }

  if (dados.pronto) {
    mostrarStorefrontPronto(dados.url);
    return;
  }

  // Ainda não está pronto: mostra o motivo e volta a perguntar.
  caixa.style.display = 'inline-flex';
  link.hidden = true;
  if (texto) {
    texto.textContent =
      dados.detalhe ||
      'A preparar o seu endereço. Pode entretanto começar a criar o seu menu.';
  }

  tentativasStorefront++;
  if (tentativasStorefront >= MAX_TENTATIVAS_STOREFRONT) {
    pararVigilanciaStorefront();
    if (texto) {
      texto.textContent =
        'O endereço está a demorar mais do que o normal. Tente abrir o link; se falhar, contacte o suporte.';
    }
    const spinner = caixa.querySelector('.spinner-espera');
    if (spinner) spinner.style.display = 'none';
    link.hidden = false;
    return;
  }

  if (!timerStorefront) {
    timerStorefront = setInterval(vigiarStorefront, 2000);
  }
}

function mostrarStorefrontPronto(url) {
  pararVigilanciaStorefront();

  const caixa = document.getElementById('storefront-estado');
  const link = document.getElementById('lbl-subdomain-link');
  if (caixa) caixa.style.display = 'none';
  if (link) {
    if (url) {
      link.href = url;
      link.innerText = url;
    }
    link.hidden = false;
  }
}

function pararVigilanciaStorefront() {
  if (timerStorefront) {
    clearInterval(timerStorefront);
    timerStorefront = null;
  }
}

// --- Utilizadores ---

const ETIQUETAS_ROLE = {
  owner: 'Proprietário',
  admin: 'Administrador',
  gerente: 'Gerente',
  funcionario: 'Funcionário',
};

async function loadUsers() {
  if (!Sessao.temSessao()) return;
  try {
    users = (await api('/api/admin/usuarios')) || [];
    renderUsers();
  } catch (err) {
    if (err.status === 403) {
      showToast('Não tem permissões para gerir utilizadores.', 'error');
      return;
    }
    showToast('Não foi possível carregar os utilizadores.', 'error');
  }
}

function renderUsers() {
  const container = document.getElementById('users-list-table');
  if (!container) return;

  if (users.length === 0) {
    container.innerHTML =
      '<tr><td colspan="5" style="text-align:center; padding: 2rem;">Nenhum utilizador registado.</td></tr>';
    return;
  }

  container.innerHTML = users
    .map((user) => {
      const estado = user.ativo
        ? '<span style="color:var(--success);">✓ Activo</span>'
        : '<span style="color:var(--text-muted);">Inactivo</span>';
      const isOwner = user.role === 'owner';
      const etiqueta = ETIQUETAS_ROLE[user.role] || user.role;

      const botao = isOwner
        ? '<span style="font-size:0.8rem; color:var(--text-muted); font-style:italic;">Proprietário</span>'
        : `<button class="btn btn-outline" style="padding: 0.25rem 0.5rem; font-size: 0.8rem; border-color: rgba(255, 71, 87, 0.3); color: #ff4757;" data-remover="${esc(user.id)}">Remover</button>`;

      return `
      <tr style="border-bottom: 1px solid var(--border-color);">
        <td style="padding: 0.75rem 0.5rem; font-weight:600; color:#fff;">${esc(user.nome)}</td>
        <td style="padding: 0.75rem 0.5rem;">${esc(user.email)}</td>
        <td style="padding: 0.75rem 0.5rem;"><span class="badge ${isOwner ? 'badge-primary' : 'badge-secondary'}" style="font-size:0.75rem; text-transform:uppercase;">${esc(etiqueta)}</span></td>
        <td style="padding: 0.75rem 0.5rem;">${estado}</td>
        <td style="padding: 0.75rem 0.5rem; text-align: right;">${botao}</td>
      </tr>`;
    })
    .join('');

  container.querySelectorAll('[data-remover]').forEach((b) => {
    b.addEventListener('click', () => deleteUser(Number(b.dataset.remover)));
  });
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

  const payload = {
    nome: document.getElementById('user-nome').value.trim(),
    email: document.getElementById('user-email').value.trim(),
    password: document.getElementById('user-password').value,
    role: document.getElementById('user-role').value,
  };

  if (payload.password.length < 8) {
    showToast('A senha tem de ter pelo menos 8 caracteres, com letras e números.', 'error');
    return;
  }

  try {
    await api('/api/admin/usuarios', { metodo: 'POST', corpo: payload });
    showToast('Utilizador criado.', 'success');
    closeUserModal();
    loadUsers();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

async function deleteUser(id) {
  if (!confirm('Remover este utilizador do painel?')) return;
  try {
    await api(`/api/admin/usuarios/${id}`, { metodo: 'DELETE' });
    showToast('Utilizador removido.', 'success');
    loadUsers();
  } catch (err) {
    showToast(err.message, 'error');
  }
}

// --- Eventos ---

// ligar associa um handler só se o elemento existir, para que uma diferença entre o HTML e
// este ficheiro não interrompa a inicialização de todo o painel.
function ligar(id, evento, handler) {
  const el = document.getElementById(id);
  if (el) el.addEventListener(evento, handler);
}

function setupEventListeners() {
  ligar('login-form', 'submit', handleLogin);
  ligar('nav-logout-btn', 'click', handleLogout);
  ligar('link-esqueci-senha', 'click', (e) => {
    e.preventDefault();
    handleEsqueciSenha();
  });

  ['pedidos', 'cardapio', 'pagamentos', 'usuarios', 'configuracoes'].forEach((nome) => {
    ligar(`nav-${nome}`, 'click', () => switchTab(nome));
  });

  ligar('btn-add-item', 'click', () => openItemModal());
  ligar('modal-close-btn', 'click', closeItemModal);
  ligar('item-form', 'submit', handleSaveItem);
  ligar('item-desconto-checkbox', 'change', (e) => {
    toggleDiscountInput(e.target);
    actualizarDetalheIVA();
  });
  ligar('item-preco', 'input', actualizarDetalheIVA);
  ligar('item-preco-desconto', 'input', actualizarDetalheIVA);
  SegGroup.ligar('item-taxa-iva', actualizarDetalheIVA);
  SegGroup.ligar('config-taxa-iva-omissao');

  ligar('payments-form', 'submit', handleSavePayments);
  ligar('btn-refresh-orders', 'click', loadOrders);
  ligar('item-modal-cancel-btn', 'click', closeItemModal);

  ligar('btn-add-user', 'click', openUserModal);
  ligar('user-modal-close-btn', 'click', closeUserModal);
  ligar('user-modal-cancel-btn', 'click', closeUserModal);
  ligar('user-form', 'submit', handleSaveUser);

  ligar('general-config-form', 'submit', handleSaveGeneralConfig);

  // Selectores de imagem: computador ou telemóvel (galeria e câmara).
  ligarSelectorDeImagem({
    botaoId: 'btn-escolher-logo', inputId: 'config-logo-ficheiro',
    previewId: 'preview-logo', urlId: 'config-logo-url',
    estadoId: 'estado-logo', removerId: 'btn-remover-logo', uso: 'logo',
  });
  ligarSelectorDeImagem({
    botaoId: 'btn-escolher-item-imagem', inputId: 'item-imagem-ficheiro',
    previewId: 'preview-item-imagem', urlId: 'item-imagem-url',
    estadoId: 'estado-item-imagem', removerId: 'btn-remover-item-imagem', uso: 'produto',
  });

  // Identidade visual: selector e campo hexadecimal mantêm-se sincronizados.
  ligar('config-cor-primaria', 'input', (e) => {
    definirValor('config-cor-primaria-hex', e.target.value);
    actualizarPreviewCor();
  });
  ligar('config-cor-primaria-hex', 'input', () => {
    const v = valorDe('config-cor-primaria-hex');
    if (/^#[0-9a-fA-F]{6}$/.test(v)) definirValor('config-cor-primaria', v);
    actualizarPreviewCor();
  });
  ligar('btn-limpar-cor', 'click', () => {
    definirValor('config-cor-primaria-hex', '');
    actualizarPreviewCor();
    showToast('A cor volta ao padrão ao guardar.', 'info');
  });
  ligar('btn-save-domain', 'click', handleSaveDomain);
  ligar('btn-check-dns', 'click', handleVerifyDomain);
}
