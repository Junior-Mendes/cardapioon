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

let menuItems = [];
let orders = [];
let users = [];
let editingItemId = null;
// Taxa de IVA sugerida pelo restaurante para produtos novos, em pontos base.
let taxaIVAOmissao = 1300;
let timerPedidos = null;

document.addEventListener('DOMContentLoaded', () => {
  registarServiceWorker();
  prepararInstalacao();
  carregarBrandingPublico();
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
  iniciarTempoReal();
  inicializarSubscricaoPush();
  iniciarBrandingAdmin();

  // Registo acabado de fazer: abre as Configurações, onde o estado do endereço é
  // mostrado, em vez de deixar o lojista num quadro de encomendas vazio sem saber se
  // a loja já está no ar.
  if (new URLSearchParams(window.location.search).get('novo') === '1') {
    switchTab('configuracoes');
    showToast('Bem-vindo! Estamos a preparar o seu endereço.', 'info');
    // Limpa o parâmetro para que um refresh não repita a mensagem.
    window.history.replaceState({}, '', '/admin');
  }

  // Sondagem de segurança, agora a cada dois minutos em vez de quinze segundos.
  //
  // O tempo real vem do stream de eventos; esta sondagem existe apenas para o caso de o
  // stream cair sem que o cliente perceba, e para corrigir divergências. Sem ela, uma
  // falha silenciosa do stream deixaria o painel congelado sem que ninguém notasse.
  if (!timerPedidos) {
    timerPedidos = setInterval(loadOrders, 120000);
  }
}

// --- Tempo real ---

// pendentesConhecidos guarda o número de encomendas à espera, para o aviso do separador.
let pendentesConhecidos = 0;

// iniciarTempoReal liga o stream de eventos e o aviso sonoro.
function iniciarTempoReal() {
  Eventos.em('encomenda_nova', (ev) => {
    const d = ev.dados || {};

    // Toca e mostra o aviso antes de recarregar a lista: a informação essencial já vem no
    // evento, e esperar pela lista atrasaria o som.
    Som.tocar();
    showToast(
      `Nova encomenda #${d.numero} — ${d.valor_total_texto || ''}`.trim(),
      'success'
    );

    loadOrders();
  });

  Eventos.em('encomenda_estado', () => {
    // Outro posto ao balcão mudou o estado: alinhar sem esperar pela sondagem.
    loadOrders();
  });

  Eventos.em('__estado', mostrarEstadoTempoReal);

  Eventos.ligar();
  actualizarBotaoSom();
}

function mostrarEstadoTempoReal(ligado) {
  const el = document.getElementById('estado-tempo-real');
  if (!el) return;

  el.classList.toggle('ligado', ligado);
  el.title = ligado
    ? 'A receber encomendas em tempo real'
    : 'Sem ligação em tempo real. A tentar reconectar; as encomendas continuam a ser recebidas.';
  el.textContent = ligado ? 'Em direto' : 'A reconectar…';
}

// --- Som de aviso ---

// activarSom tem de correr dentro de um gesto do utilizador: os browsers bloqueiam áudio
// até haver interação, e sem isto o painel ficaria mudo sem o lojista saber.
function activarSom() {
  const pronto = Som.armar();
  Som.definirPreferido(pronto);
  actualizarBotaoSom();

  if (pronto) {
    // Toca uma vez para o lojista confirmar que ouve, e a que volume.
    Som.tocar({ repeticoes: 1 });
    showToast('Aviso sonoro activado.', 'success');
  } else {
    showToast('O navegador não permitiu activar o som nesta página.', 'error');
  }
}

function desactivarSom() {
  Som.definirPreferido(false);
  actualizarBotaoSom();
  showToast('Aviso sonoro desligado.', 'info');
}

// actualizarBotaoSom mantém o botão a dizer a verdade sobre o estado do som.
//
// Três estados distintos, e a distinção importa: desligado por escolha, ligado e a
// funcionar, ou ligado na preferência mas ainda sem gesto — este último é o perigoso,
// porque o lojista pensa que vai ser avisado.
function actualizarBotaoSom() {
  const btn = document.getElementById('btn-som');
  if (!btn) return;

  if (!Som.preferido()) {
    btn.textContent = '🔇 Som desligado';
    btn.classList.remove('som-activo', 'som-pendente');
    return;
  }
  if (Som.armado) {
    btn.textContent = '🔔 Som ligado';
    btn.classList.add('som-activo');
    btn.classList.remove('som-pendente');
    return;
  }
  btn.textContent = '🔔 Toque para activar o som';
  btn.classList.add('som-pendente');
  btn.classList.remove('som-activo');
}

function alternarSom() {
  if (Som.preferido() && Som.armado) desactivarSom();
  else activarSom();
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
  Eventos.desligar();
  Titulo.parar();
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

  ['pedidos', 'cardapio', 'pagamentos', 'usuarios', 'configuracoes'].forEach((nome) => {
    const painel = document.getElementById(`tab-${nome}`);
    if (painel) painel.style.display = tab === nome ? 'block' : 'none';

    const nav = document.getElementById(`nav-${nome}`);
    if (nav) nav.classList.toggle('active', tab === nome);
  });

  // Fechar menu lateral no mobile após mudar de aba
  const sidebar = document.querySelector('.sidebar');
  const backdrop = document.getElementById('sidebar-backdrop');
  if (sidebar) sidebar.classList.remove('open');
  if (backdrop) backdrop.classList.remove('active');

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

  // Aviso no separador do browser: o som pode não bastar numa cozinha com ruído, e o
  // painel pode estar num separador de fundo.
  pendentesConhecidos = orders.filter((o) => o.status === 'pendente').length;
  Titulo.actualizar(pendentesConhecidos);
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
        <span>
          ${esc(i.quantidade)}x ${esc(i.nome)}
          ${i.observacoes ? `<em class="order-item-obs">${esc(i.observacoes)}</em>` : ''}
        </span>
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
    const tabEl = document.getElementById(`tab-count-${k}`);
    if (tabEl) tabEl.innerText = contagens[k];
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

// Termo de pesquisa do menu no painel.
let buscaMenu = '';

// Categorias recolhidas pelo lojista, guardadas na sessão para que o estado sobreviva a um
// refresh — quem trabalha com uma categoria não quer reabri-la a cada recarregamento.
const categoriasFechadas = new Set(
  JSON.parse(sessionStorage.getItem('cardapio_categorias_fechadas') || '[]')
);

function guardarCategoriasFechadas() {
  sessionStorage.setItem(
    'cardapio_categorias_fechadas',
    JSON.stringify([...categoriasFechadas])
  );
}

// renderMenuGrid desenha o menu do painel agrupado por categoria.
//
// Antes era uma grelha plana de cartões grandes: com trinta pratos, encontrar um para
// mudar o preço obrigava a percorrer tudo, e não se percebia a estrutura que o cliente vê.
// Agora são secções recolhíveis com linhas compactas, na mesma ordem do menu público.
function renderMenuGrid() {
  const container = document.getElementById('menu-items-admin');
  if (!container) return;

  if (menuItems.length === 0) {
    container.innerHTML =
      '<div class="cart-empty glass"><p>O seu menu está vazio. Adicione o primeiro prato para começar.</p></div>';
    actualizarResumoMenu();
    return;
  }

  const filtrados = filtrarMenuAdmin();

  if (filtrados.length === 0) {
    container.innerHTML = `
      <div class="cart-empty glass">
        <p>Nenhum prato corresponde a &laquo;${esc(buscaMenu)}&raquo;.</p>
      </div>`;
    actualizarResumoMenu();
    return;
  }

  // Agrupa preservando a ordem em que as categorias aparecem.
  const porCategoria = new Map();
  filtrados.forEach((item) => {
    if (!porCategoria.has(item.categoria)) porCategoria.set(item.categoria, []);
    porCategoria.get(item.categoria).push(item);
  });

  container.innerHTML = [...porCategoria.entries()]
    .map(([categoria, itens]) => seccaoMenuHTML(categoria, itens))
    .join('');

  ligarAccoesDoMenu(container);
  actualizarResumoMenu();
}

function filtrarMenuAdmin() {
  if (!buscaMenu) return menuItems;

  const t = normalizarTexto(buscaMenu);
  return menuItems.filter(
    (i) =>
      normalizarTexto(i.nome).includes(t) ||
      normalizarTexto(i.categoria).includes(t) ||
      normalizarTexto(i.descricao || '').includes(t)
  );
}

// normalizarTexto remove acentos, para que "frances" encontre "Francesinha".
function normalizarTexto(texto) {
  return String(texto)
    .toLowerCase()
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '');
}

function seccaoMenuHTML(categoria, itens) {
  const pausados = itens.filter((i) => !i.disponivel).length;
  // Com pesquisa activa todas as secções abrem: esconder resultados atrás de um clique
  // anularia a pesquisa.
  const fechada = !buscaMenu && categoriasFechadas.has(categoria);

  const resumo = [
    `${itens.length} ${itens.length === 1 ? 'prato' : 'pratos'}`,
    pausados > 0 ? `${pausados} em pausa` : '',
  ]
    .filter(Boolean)
    .join(' · ');

  return `
    <section class="menu-cat">
      <button type="button" class="menu-cat-cabecalho" data-categoria="${escAttr(categoria)}"
              aria-expanded="${fechada ? 'false' : 'true'}">
        <span class="menu-cat-seta" aria-hidden="true">${fechada ? '▸' : '▾'}</span>
        <span class="menu-cat-nome">${esc(categoria)}</span>
        <span class="menu-cat-resumo">${esc(resumo)}</span>
      </button>
      <div class="menu-cat-corpo"${fechada ? ' hidden' : ''}>
        ${itens.map(linhaMenuHTML).join('')}
      </div>
    </section>`;
}

// linhaMenuHTML desenha um prato como linha, e não como cartão.
//
// A linha mostra tudo o que o lojista precisa de ver de relance — nome, preço, taxa de IVA,
// se está em pausa — e o interruptor de disponibilidade fica ali, sem abrir nada.
function linhaMenuHTML(item) {
  const temDesconto = item.desconto_ativo && Number(item.preco_desconto_cents) > 0;

  const preco = temDesconto
    ? `<span class="linha-preco-antigo">${esc(formatCents(item.preco_cents))}</span>
       <span class="linha-preco">${esc(formatCents(item.preco_desconto_cents))}</span>`
    : `<span class="linha-preco">${esc(formatCents(item.preco_cents))}</span>`;

  const miniatura = item.imagem_url
    ? `<span class="linha-img" style="background-image:url('${escAttr(item.imagem_url)}')"></span>`
    : `<span class="linha-img linha-img-vazia" aria-hidden="true">🍽</span>`;

  const taxa = item.taxa_iva_bp > 0 ? `IVA ${item.taxa_iva_bp / 100}%` : 'Isento';

  return `
    <div class="menu-linha${item.disponivel ? '' : ' menu-linha-pausada'}" data-id="${esc(item.id)}">
      ${miniatura}

      <div class="linha-info">
        <div class="linha-nome">${esc(item.nome)}</div>
        <div class="linha-meta">
          ${preco}
          <span class="linha-taxa">${esc(taxa)}</span>
          ${item.disponivel ? '' : '<span class="linha-badge-pausa">Em pausa</span>'}
        </div>
      </div>

      <div class="linha-accoes">
        <label class="switch" title="${item.disponivel ? 'Pausar este prato' : 'Voltar a mostrar'}">
          <input type="checkbox" data-disponivel="${esc(item.id)}" ${item.disponivel ? 'checked' : ''}>
          <span class="slider"></span>
        </label>
        <button type="button" class="btn btn-secondary linha-btn" data-editar="${esc(item.id)}">Editar</button>
        <button type="button" class="btn btn-outline linha-btn linha-btn-perigo" data-apagar="${esc(item.id)}">Eliminar</button>
      </div>
    </div>`;
}

function ligarAccoesDoMenu(container) {
  container.querySelectorAll('[data-categoria]').forEach((b) => {
    b.addEventListener('click', () => alternarCategoria(b.dataset.categoria));
  });
  container.querySelectorAll('[data-editar]').forEach((b) => {
    b.addEventListener('click', () => openItemModal(Number(b.dataset.editar)));
  });
  container.querySelectorAll('[data-apagar]').forEach((b) => {
    b.addEventListener('click', () => deleteMenuItem(Number(b.dataset.apagar)));
  });
  container.querySelectorAll('[data-disponivel]').forEach((cb) => {
    cb.addEventListener('change', () =>
      alternarDisponibilidade(Number(cb.dataset.disponivel), cb.checked, cb)
    );
  });
}

function alternarCategoria(categoria) {
  if (categoriasFechadas.has(categoria)) categoriasFechadas.delete(categoria);
  else categoriasFechadas.add(categoria);
  guardarCategoriasFechadas();
  renderMenuGrid();
}

// alternarDisponibilidade pausa ou retoma um prato num toque.
//
// É a operação mais urgente do serviço: o prato acabou e tem de sair do menu já. Por isso
// não passa pelo formulário completo, e a interface reage de imediato — se o servidor
// falhar, o interruptor volta atrás.
async function alternarDisponibilidade(id, disponivel, checkbox) {
  checkbox.disabled = true;
  try {
    await api(`/api/admin/cardapio/${id}/disponibilidade`, {
      metodo: 'PATCH',
      corpo: { disponivel },
    });

    const item = menuItems.find((i) => i.id === id);
    if (item) item.disponivel = disponivel;

    showToast(disponivel ? 'Prato disponível' : 'Prato em pausa', 'success');
    renderMenuGrid();
  } catch (err) {
    // Reverte para o estado real: deixar o interruptor na posição nova daria a impressão
    // de que o prato foi pausado quando continua à venda.
    checkbox.checked = !disponivel;
    checkbox.disabled = false;
    showToast(err.message, 'error');
  }
}

function actualizarResumoMenu() {
  const el = document.getElementById('menu-admin-resumo');
  if (!el) return;

  const total = menuItems.length;
  const pausados = menuItems.filter((i) => !i.disponivel).length;
  const categorias = new Set(menuItems.map((i) => i.categoria)).size;

  if (total === 0) {
    el.textContent = '';
    return;
  }

  const partes = [
    `${total} ${total === 1 ? 'prato' : 'pratos'}`,
    `${categorias} ${categorias === 1 ? 'categoria' : 'categorias'}`,
  ];
  if (pausados > 0) partes.push(`${pausados} em pausa`);
  el.textContent = partes.join(' · ');
}

function ligarBuscaMenuAdmin() {
  const campo = document.getElementById('menu-admin-busca');
  const limpar = document.getElementById('menu-admin-busca-limpar');
  if (!campo) return;

  let atrasado = null;
  campo.addEventListener('input', () => {
    clearTimeout(atrasado);
    atrasado = setTimeout(() => {
      buscaMenu = campo.value.trim();
      if (limpar) limpar.hidden = buscaMenu === '';
      renderMenuGrid();
    }, 150);
  });

  if (limpar) {
    limpar.addEventListener('click', () => {
      campo.value = '';
      buscaMenu = '';
      limpar.hidden = true;
      renderMenuGrid();
      campo.focus();
    });
  }
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
    aplicarBrandingAdmin(config);

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
      <table class="tabela-rolavel" style="width:100%; font-size:0.85rem; margin-bottom:0.75rem;">
        <tr><td style="padding:2px 8px 2px 0;">Tipo</td><td><code>${esc(dados.registo_txt.tipo)}</code></td></tr>
        <tr><td style="padding:2px 8px 2px 0;">Nome</td><td><code>${esc(dados.registo_txt.nome)}</code></td></tr>
        <tr><td style="padding:2px 8px 2px 0;">Valor</td><td><code>${esc(dados.registo_txt.valor)}</code></td></tr>
      </table>
      <p style="margin:0.5rem 0;">E este, para encaminhar o tráfego:</p>
      <table class="tabela-rolavel" style="width:100%; font-size:0.85rem;">
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
  ligar('btn-som', 'click', alternarSom);
  ligar('btn-instalar', 'click', instalarApp);
  ligar('link-esqueci-senha', 'click', (e) => {
    e.preventDefault();
    handleEsqueciSenha();
  });

  ['pedidos', 'cardapio', 'pagamentos', 'usuarios', 'configuracoes'].forEach((nome) => {
    ligar(`nav-${nome}`, 'click', () => switchTab(nome));
  });

  ligarBuscaMenuAdmin();
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

  setupMobileMenu();
  setupKanbanTabs();
}

function setupMobileMenu() {
  const toggleBtn = document.getElementById('mobile-menu-toggle');
  const sidebar = document.querySelector('.sidebar');
  const backdrop = document.getElementById('sidebar-backdrop');

  if (toggleBtn && sidebar && backdrop) {
    toggleBtn.addEventListener('click', () => {
      sidebar.classList.toggle('open');
      backdrop.classList.toggle('active');
    });

    backdrop.addEventListener('click', () => {
      sidebar.classList.remove('open');
      backdrop.classList.remove('active');
    });
  }
}

function setupKanbanTabs() {
  const tabsContainer = document.getElementById('kanban-tabs-container');
  if (!tabsContainer) return;

  tabsContainer.querySelectorAll('.kanban-tab-btn').forEach((btn) => {
    btn.addEventListener('click', () => {
      const targetCol = btn.dataset.coluna;

      // Alternar classe active nos botões
      tabsContainer.querySelectorAll('.kanban-tab-btn').forEach((t) => {
        t.classList.toggle('active', t === btn);
      });

      // Alternar classe active nas colunas do quadro
      ['pendente', 'preparando', 'pronto', 'finalizado'].forEach((col) => {
        const el = document.getElementById(`col-${col}`);
        if (el) el.classList.toggle('active', col === targetCol);
      });
    });
  });
}

// --- Branding Dinâmico ---

let brandingAplicado = false;

function calcularIniciais(nome) {
  if (!nome) return '?';
  const palavras = nome.split(/\s+/);
  const iniciais = [];
  const stopWords = ['do', 'da', 'de', 'dos', 'das', 'e'];

  for (const palavra of palavras) {
    if (!palavra) continue;
    const minuscula = palavra.toLowerCase();
    if (iniciais.length > 0 && stopWords.includes(minuscula)) {
      continue;
    }
    iniciais.push(palavra[0].toUpperCase());
    if (iniciais.length === 2) break;
  }

  return iniciais.length > 0 ? iniciais.join('') : '?';
}

function aplicarBrandingAdmin(config) {
  if (!config) return;

  // 1. Atualizar logótipos (.logo-icon)
  const icones = document.querySelectorAll('.logo-icon');
  icones.forEach((icone) => {
    if (icone.id === 'preview-logo') return;

    if (config.logo_url) {
      icone.textContent = '';
      icone.style.backgroundImage = `url('${config.logo_url}')`;
      icone.style.backgroundSize = 'cover';
      icone.style.backgroundPosition = 'center';
      icone.style.borderRadius = 'var(--radius-sm)';
    } else {
      icone.style.backgroundImage = 'none';
      icone.textContent = config.iniciais || calcularIniciais(config.nome);
    }
  });

  // 2. Atualizar o texto do logótipo (.logo-text)
  const textos = document.querySelectorAll('.logo-text');
  textos.forEach((texto) => {
    if (config.nome) {
      texto.textContent = config.nome;
    }
  });

  // 3. Atualizar cores primárias no CSS
  if (config.cor_primaria) {
    const raiz = document.documentElement;
    raiz.style.setProperty('--primary', config.cor_primaria);

    const componentes = (hex) => {
      const h = String(hex).replace('#', '');
      if (h.length !== 6) return null;
      return [
        parseInt(h.slice(0, 2), 16),
        parseInt(h.slice(2, 4), 16),
        parseInt(h.slice(4, 6), 16),
      ];
    };

    const paraHex = ([r, g, b]) => {
      const clamp = (v) => Math.max(0, Math.min(255, Math.round(v)));
      return '#' + [r, g, b].map((v) => clamp(v).toString(16).padStart(2, '0')).join('');
    };

    const escurecer = (hex, fraccao) => {
      const c = componentes(hex);
      return c ? paraHex(c.map((v) => v * (1 - fraccao))) : hex;
    };

    const clarear = (hex, fraccao) => {
      const c = componentes(hex);
      return c ? paraHex(c.map((v) => v + (255 - v) * fraccao)) : hex;
    };

    raiz.style.setProperty('--primary-dark', escurecer(config.cor_primaria, 0.15));
    raiz.style.setProperty('--primary-light', clarear(config.cor_primaria, 0.2));
    if (config.cor_texto_sobre_primaria) {
      raiz.style.setProperty('--on-primary', config.cor_texto_sobre_primaria);
    }
  }

  // 4. Favicon
  if (config.logo_url) {
    let link = document.querySelector('link[rel="icon"]');
    if (!link) {
      link = document.createElement('link');
      link.setAttribute('rel', 'icon');
      document.head.appendChild(link);
    }
    link.setAttribute('href', config.logo_url);
  }

  // 5. Título da aba do browser
  if (config.nome) {
    document.title = `${config.nome} — Painel Administrativo`;
  }
}

async function iniciarBrandingAdmin() {
  if (brandingAplicado) return;
  try {
    const config = await api('/api/admin/config');
    aplicarBrandingAdmin(config);
    brandingAplicado = true;
  } catch (err) {
    console.warn('Erro ao carregar branding autenticado do admin:', err);
  }
}

async function carregarBrandingPublico() {
  try {
    const dados = await api('/api/public-menu', { autenticado: false });
    if (dados && dados.restaurante) {
      aplicarBrandingAdmin(dados.restaurante);
    }
  } catch (err) {
    console.warn('Branding público não carregado:', err);
  }
}
