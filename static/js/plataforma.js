// Consola da plataforma — o painel de quem opera o SaaS.
//
// Sessão e cliente de API próprios, e não os de common.js, por duas razões concretas:
//
//  1. As chaves de localStorage são diferentes. Com as mesmas chaves, abrir esta consola
//     substituiria a sessão do painel do lojista no mesmo browser (e o contrário), o que na
//     prática expulsaria o utilizador de um dos dois de cada vez que trocasse de aba.
//  2. A renovação usa /api/plataforma/refresh. A função de common.js aponta para
//     /api/tenant/refresh, que não conhece estes tokens.
//
// De common.js consome apenas os utilitários: esc, formatCents, formatDateTime e showToast.

'use strict';

const CHAVE_ACCESS_P = 'plataforma_access_token';
const CHAVE_REFRESH_P = 'plataforma_refresh_token';
const CHAVE_ADMIN_P = 'plataforma_admin';

const SessaoPlataforma = {
  guardar(dados) {
    localStorage.setItem(CHAVE_ACCESS_P, dados.access_token);
    localStorage.setItem(CHAVE_REFRESH_P, dados.refresh_token);
    localStorage.setItem(
      CHAVE_ADMIN_P,
      JSON.stringify({
        nome: dados.admin?.nome || '',
        email: dados.admin?.email || '',
      })
    );
  },

  accessToken() {
    return localStorage.getItem(CHAVE_ACCESS_P);
  },

  refreshToken() {
    return localStorage.getItem(CHAVE_REFRESH_P);
  },

  info() {
    try {
      return JSON.parse(localStorage.getItem(CHAVE_ADMIN_P) || '{}');
    } catch {
      return {};
    }
  },

  temSessao() {
    return Boolean(this.accessToken());
  },

  limpar() {
    localStorage.removeItem(CHAVE_ACCESS_P);
    localStorage.removeItem(CHAVE_REFRESH_P);
    localStorage.removeItem(CHAVE_ADMIN_P);
  },
};

// --- Cliente de API ---

let renovacaoPlataforma = null;

// Chamadas concorrentes partilham a mesma promessa: o servidor revoga um refresh token
// reapresentado, pelo que gastá-lo duas vezes terminaria a sessão.
async function renovarPlataforma() {
  if (renovacaoPlataforma) return renovacaoPlataforma;

  const refresh = SessaoPlataforma.refreshToken();
  if (!refresh) return null;

  renovacaoPlataforma = (async () => {
    try {
      const res = await fetch('/api/plataforma/refresh', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: refresh }),
      });
      if (!res.ok) return null;

      const dados = await res.json();
      localStorage.setItem(CHAVE_ACCESS_P, dados.access_token);
      localStorage.setItem(CHAVE_REFRESH_P, dados.refresh_token);
      return dados.access_token;
    } catch {
      return null;
    } finally {
      renovacaoPlataforma = null;
    }
  })();

  return renovacaoPlataforma;
}

class ErroPlataforma extends Error {
  constructor(mensagem, status) {
    super(mensagem);
    this.status = status;
  }
}

async function apiP(caminho, opcoes = {}) {
  const { metodo = 'GET', corpo = null, autenticado = true } = opcoes;

  const executar = async () => {
    const headers = {};
    if (corpo !== null) headers['Content-Type'] = 'application/json';
    if (autenticado) {
      const tok = SessaoPlataforma.accessToken();
      if (tok) headers['Authorization'] = `Bearer ${tok}`;
    }
    return fetch(caminho, {
      method: metodo,
      headers,
      body: corpo !== null ? JSON.stringify(corpo) : undefined,
    });
  };

  let res = await executar();

  if (res.status === 401 && autenticado && SessaoPlataforma.refreshToken()) {
    if (await renovarPlataforma()) res = await executar();
  }

  if (res.status === 401 && autenticado) {
    SessaoPlataforma.limpar();
    mostrarEntrada();
    throw new ErroPlataforma('A sessão terminou. Entre novamente.', 401);
  }

  let dados = null;
  if ((res.headers.get('Content-Type') || '').includes('application/json')) {
    dados = await res.json().catch(() => null);
  }
  if (!res.ok) {
    throw new ErroPlataforma(dados?.error || 'Não foi possível concluir a operação.', res.status);
  }
  return dados;
}

// --- Estado ---

const estado = {
  vista: 'resumo',
  lista: { pagina: 1, total: 0, porPagina: 20 },
  auditoria: { pagina: 1, total: 0, porPagina: 50 },
  detalheID: null,
  mainDomain: '',
};

// Devolve um elemento pelo id. Encurta o código de render, que é quase todo DOM.
const $ = (id) => document.getElementById(id);

// --- Formatação ---

function formatNumero(n) {
  return new Intl.NumberFormat('pt-PT').format(Number(n) || 0);
}

// diaCurto transforma "2026-08-06" em "06/08", para o eixo do gráfico.
function diaCurto(iso) {
  const partes = String(iso || '').split('-');
  return partes.length === 3 ? `${partes[2]}/${partes[1]}` : '';
}

// desdeQuando dá a distância em linguagem corrente. "Há 3 dias" diz mais sobre a saúde de
// uma conta do que uma data que obriga a fazer a subtracção de cabeça.
function desdeQuando(valor) {
  if (!valor) return 'Nunca';
  const d = new Date(valor);
  if (Number.isNaN(d.getTime())) return '—';

  const minutos = Math.floor((Date.now() - d.getTime()) / 60000);
  if (minutos < 1) return 'Agora mesmo';
  if (minutos < 60) return `Há ${minutos} min`;
  const horas = Math.floor(minutos / 60);
  if (horas < 24) return `Há ${horas} h`;
  const dias = Math.floor(horas / 24);
  if (dias === 1) return 'Ontem';
  if (dias < 30) return `Há ${dias} dias`;
  const meses = Math.floor(dias / 30);
  return meses === 1 ? 'Há 1 mês' : `Há ${meses} meses`;
}

function seloEstado(ativo) {
  return ativo
    ? '<span class="p-selo p-selo-activo">Activo</span>'
    : '<span class="p-selo p-selo-suspenso">Suspenso</span>';
}

// enderecoDe devolve o endereço público do restaurante: o domínio próprio quando está
// verificado, e o subdomínio da plataforma nos restantes casos.
function enderecoDe(r) {
  if (r.domain && r.domain_status === 'verified') return r.domain;
  return estado.mainDomain ? `${r.slug}.${estado.mainDomain}` : r.slug;
}

function seloDominio(r) {
  switch (r.domain_status) {
    case 'verified':
      return '<span class="p-selo p-selo-info">Domínio próprio</span>';
    case 'pending':
      return '<span class="p-selo p-selo-aviso">Domínio a verificar</span>';
    default:
      return '';
  }
}

function linhaVazia(colunas, mensagem) {
  return `<tr><td colspan="${colunas}" class="p-vazio">${esc(mensagem)}</td></tr>`;
}

// --- Navegação entre vistas ---

const VISTAS = ['resumo', 'restaurantes', 'auditoria'];

function mostrarVista(vista) {
  if (!VISTAS.includes(vista)) return;
  estado.vista = vista;

  VISTAS.forEach((v) => {
    $(`vista-${v}`).hidden = v !== vista;
  });
  document.querySelectorAll('.p-nav-btn[data-vista]').forEach((btn) => {
    btn.classList.toggle('activo', btn.dataset.vista === vista);
  });

  if (vista === 'resumo') carregarResumo();
  if (vista === 'restaurantes') carregarRestaurantes();
  if (vista === 'auditoria') carregarAuditoria();
}

function mostrarEntrada() {
  $('login-overlay').hidden = false;
  $('app').hidden = true;
}

function mostrarConsola() {
  $('login-overlay').hidden = true;
  $('app').hidden = false;

  const info = SessaoPlataforma.info();
  $('admin-nome').textContent = info.nome || 'Administrador';
  $('admin-email').textContent = info.email || '';
}

// --- Resumo ---

async function carregarResumo() {
  try {
    const d = await apiP('/api/plataforma/resumo');
    estado.mainDomain = d.main_domain || '';

    $('resumo-sub').textContent =
      `${formatNumero(d.restaurantes)} restaurantes registados · ` +
      `${formatNumero(d.com_vendas_7d)} com vendas nos últimos 7 dias`;

    const kpis = [
      {
        etiqueta: 'Restaurantes activos',
        valor: formatNumero(d.restaurantes_activos),
        nota: d.restaurantes_inactivos > 0
          ? `${formatNumero(d.restaurantes_inactivos)} suspensos`
          : 'Nenhum suspenso',
      },
      {
        etiqueta: 'Novos (30 dias)',
        valor: formatNumero(d.novos_30d),
        nota: `${formatNumero(d.com_dominio_proprio)} com domínio próprio`,
      },
      {
        etiqueta: 'Encomendas hoje',
        valor: formatNumero(d.encomendas_hoje),
        nota: `${formatCents(d.volume_hoje_cents)} facturados`,
      },
      {
        etiqueta: 'Encomendas (30 dias)',
        valor: formatNumero(d.encomendas_30d),
        nota: `${formatNumero(d.encomendas_total)} desde o início`,
      },
      {
        etiqueta: 'Volume (30 dias)',
        valor: formatCents(d.volume_30d_cents),
        // O IVA é dos restaurantes, não da plataforma: aparece aqui só como escala do
        // negócio que passa pelo sistema.
        nota: `inclui ${formatCents(d.iva_30d_cents)} de IVA dos clientes`,
      },
      {
        etiqueta: 'Contas e produtos',
        valor: formatNumero(d.utilizadores),
        nota: `${formatNumero(d.produtos)} produtos no total`,
      },
    ];

    $('resumo-kpis').innerHTML = kpis
      .map(
        (k) => `<div class="p-kpi">
          <div class="p-kpi-etiqueta">${esc(k.etiqueta)}</div>
          <div class="p-kpi-valor">${esc(k.valor)}</div>
          <div class="p-kpi-nota">${esc(k.nota)}</div>
        </div>`
      )
      .join('');

    desenharSerie(d.serie || []);
    await carregarTopClientes();
  } catch (err) {
    if (err.status !== 401) showToast(err.message, 'error');
  }
}

// desenharSerie constrói um gráfico de barras com divs.
//
// Sem biblioteca de gráficos de propósito: a CSP não permite carregar scripts de outra
// origem, e catorze barras não justificam empacotar uma.
function desenharSerie(serie) {
  const maximo = Math.max(1, ...serie.map((p) => Number(p.encomendas) || 0));
  const total = serie.reduce((soma, p) => soma + (Number(p.encomendas) || 0), 0);
  const volume = serie.reduce((soma, p) => soma + (Number(p.volume_cents) || 0), 0);

  $('serie-legenda').textContent =
    `${formatNumero(total)} encomendas · ${formatCents(volume)}`;

  $('serie-grafico').innerHTML = serie
    .map((p) => {
      const n = Number(p.encomendas) || 0;
      const altura = Math.round((n / maximo) * 100);
      const titulo = `${diaCurto(p.dia)}: ${formatNumero(n)} encomendas, ${formatCents(p.volume_cents)}`;
      return `<div class="p-barra-coluna" title="${esc(titulo)}">
          <div class="p-barra" style="height:${altura}%"></div>
          <div class="p-barra-dia">${esc(diaCurto(p.dia))}</div>
        </div>`;
    })
    .join('');
}

async function carregarTopClientes() {
  const d = await apiP('/api/plataforma/restaurantes?ordem=volume&por_pagina=5');
  const linhas = d.restaurantes || [];

  $('top-clientes').innerHTML = linhas.length
    ? linhas
        .map(
          (r) => `<tr>
            <td class="p-celula-principal">
              <strong>${esc(r.nome)}</strong>
              <span class="p-celula-secundaria">${esc(enderecoDe(r))}</span>
            </td>
            <td class="p-num">${esc(formatNumero(r.encomendas_30d))}</td>
            <td class="p-num">${esc(formatCents(r.volume_30d_cents))}</td>
            <td>${esc(desdeQuando(r.ultima_encomenda))}</td>
          </tr>`
        )
        .join('')
    : linhaVazia(4, 'Ainda não há encomendas registadas em nenhum restaurante.');
}

// --- Restaurantes ---

async function carregarRestaurantes() {
  const params = new URLSearchParams({
    pagina: String(estado.lista.pagina),
    por_pagina: String(estado.lista.porPagina),
    ordem: $('filtro-ordem').value,
  });
  const q = $('filtro-q').value.trim();
  if (q) params.set('q', q);
  const filtroEstado = $('filtro-estado').value;
  if (filtroEstado) params.set('estado', filtroEstado);

  $('lista-restaurantes').innerHTML = linhaVazia(8, 'A carregar…');

  try {
    const d = await apiP(`/api/plataforma/restaurantes?${params.toString()}`);
    estado.mainDomain = d.main_domain || estado.mainDomain;
    estado.lista.total = d.total || 0;

    const linhas = d.restaurantes || [];
    $('lista-sub').textContent = q || filtroEstado
      ? `${formatNumero(d.total)} de ${formatNumero(d.total)} restaurantes correspondem ao filtro`
      : `${formatNumero(d.total)} restaurantes registados`;

    $('lista-restaurantes').innerHTML = linhas.length
      ? linhas
          .map(
            (r) => `<tr data-id="${esc(r.id)}" tabindex="0">
              <td class="p-celula-principal">
                <strong>${esc(r.nome)}</strong>
                <span class="p-celula-secundaria">
                  ${esc(r.utilizadores ? '' : 'sem equipa · ')}${esc(r.nif || 'sem NIF')}
                </span>
              </td>
              <td>
                <div class="p-mono">${esc(enderecoDe(r))}</div>
                ${seloDominio(r)}
              </td>
              <td>${seloEstado(r.ativo)}</td>
              <td class="p-num">${esc(formatNumero(r.utilizadores))}</td>
              <td class="p-num">${esc(formatNumero(r.produtos))}</td>
              <td class="p-num">${esc(formatNumero(r.encomendas_30d))}</td>
              <td class="p-num">${esc(formatCents(r.volume_30d_cents))}</td>
              <td>${esc(formatDateTime(r.created_at))}</td>
            </tr>`
          )
          .join('')
      : linhaVazia(8, 'Nenhum restaurante corresponde a estes filtros.');

    desenharPaginacao('lista-paginacao', estado.lista, 'restaurantes');
  } catch (err) {
    if (err.status !== 401) {
      showToast(err.message, 'error');
      $('lista-restaurantes').innerHTML = linhaVazia(8, 'Não foi possível carregar a lista.');
    }
  }
}

// --- Auditoria ---

async function carregarAuditoria() {
  const params = new URLSearchParams({
    pagina: String(estado.auditoria.pagina),
    por_pagina: String(estado.auditoria.porPagina),
  });
  const acao = $('auditoria-acao').value.trim();
  if (acao) params.set('acao', acao);

  $('lista-auditoria').innerHTML = linhaVazia(6, 'A carregar…');

  try {
    const d = await apiP(`/api/plataforma/auditoria?${params.toString()}`);
    estado.auditoria.total = d.total || 0;

    const registos = d.registos || [];
    $('lista-auditoria').innerHTML = registos.length
      ? registos
          .map(
            (r) => `<tr>
              <td>${esc(formatDateTime(r.created_at))}</td>
              <td>${esc(r.restaurante_nome || '—')}</td>
              <td class="p-celula-secundaria">${esc(r.usuario_email || 'plataforma')}</td>
              <td><span class="p-mono">${esc(r.acao)}</span></td>
              <td class="p-celula-secundaria">${esc(r.detalhe || '—')}</td>
              <td class="p-mono">${esc(r.ip || '—')}</td>
            </tr>`
          )
          .join('')
      : linhaVazia(6, 'Nenhuma acção registada com estes filtros.');

    desenharPaginacao('auditoria-paginacao', estado.auditoria, 'auditoria');
  } catch (err) {
    if (err.status !== 401) {
      showToast(err.message, 'error');
      $('lista-auditoria').innerHTML = linhaVazia(6, 'Não foi possível carregar o registo.');
    }
  }
}

// --- Paginação ---

function desenharPaginacao(idContentor, alvo, tipo) {
  const paginas = Math.max(1, Math.ceil(alvo.total / alvo.porPagina));
  const contentor = $(idContentor);

  if (paginas <= 1) {
    contentor.innerHTML = '';
    return;
  }

  contentor.innerHTML = `
    <span>Página ${esc(alvo.pagina)} de ${esc(paginas)} · ${esc(formatNumero(alvo.total))} registos</span>
    <div class="p-paginacao-botoes">
      <button type="button" class="p-btn-pagina" data-pagina="anterior" data-tipo="${esc(tipo)}"
        ${alvo.pagina <= 1 ? 'disabled' : ''}>Anterior</button>
      <button type="button" class="p-btn-pagina" data-pagina="seguinte" data-tipo="${esc(tipo)}"
        ${alvo.pagina >= paginas ? 'disabled' : ''}>Seguinte</button>
    </div>`;
}

// --- Detalhe de um restaurante ---

async function abrirDetalhe(id) {
  estado.detalheID = id;
  $('detalhe-fundo').hidden = false;
  $('detalhe').innerHTML = '<p class="p-vazio">A carregar…</p>';
  $('detalhe').focus();

  try {
    const d = await apiP(`/api/plataforma/restaurantes/${encodeURIComponent(id)}`);
    desenharDetalhe(d);
  } catch (err) {
    if (err.status === 401) return;
    $('detalhe').innerHTML = `<p class="p-vazio">${esc(err.message)}</p>`;
  }
}

function fecharDetalhe() {
  $('detalhe-fundo').hidden = true;
  estado.detalheID = null;
}

function desenharDetalhe(d) {
  const r = d.restaurante;
  const m = d.metricas;
  const endereco = enderecoDe(r);

  const pares = (itens) =>
    itens
      .map(
        (i) => `<div>
          <div class="p-par-etiqueta">${esc(i[0])}</div>
          <div class="p-par-valor">${esc(i[1])}</div>
        </div>`
      )
      .join('');

  const equipa = (d.equipa || []).length
    ? (d.equipa || [])
        .map(
          (u) => `<tr>
            <td class="p-celula-principal">
              <strong>${esc(u.nome)}</strong>
              <span class="p-celula-secundaria">${esc(u.email)}</span>
            </td>
            <td><span class="p-selo p-selo-neutro">${esc(u.role)}</span></td>
            <td>${esc(desdeQuando(u.last_login_at))}</td>
          </tr>`
        )
        .join('')
    : linhaVazia(3, 'Este restaurante não tem contas de acesso.');

  const encomendas = (d.encomendas || []).length
    ? (d.encomendas || [])
        .map(
          (p) => `<tr>
            <td>${esc(formatDateTime(p.created_at))}</td>
            <td><span class="p-selo p-selo-neutro">${esc(p.status)}</span></td>
            <td class="p-celula-secundaria">${esc(p.forma_pagamento)}</td>
            <td class="p-num">${esc(formatCents(p.valor_total_cents))}</td>
          </tr>`
        )
        .join('')
    : linhaVazia(4, 'Ainda não há encomendas neste restaurante.');

  const accaoEstado = r.ativo
    ? `<button type="button" class="btn p-btn-perigo" id="btn-suspender">Suspender restaurante</button>`
    : `<button type="button" class="btn btn-primary" id="btn-reactivar">Reactivar restaurante</button>`;

  $('detalhe').innerHTML = `
    <div class="p-gaveta-topo">
      <div>
        <h2 id="detalhe-titulo">${esc(r.nome)}</h2>
        <p class="p-celula-secundaria">
          <span class="p-mono">${esc(endereco)}</span> · ${seloEstado(r.ativo)} ${seloDominio(r)}
        </p>
      </div>
      <button type="button" class="p-fechar" id="detalhe-fechar" aria-label="Fechar">✕</button>
    </div>

    <div class="p-secao">
      <h3>Actividade</h3>
      <div class="p-pares">
        ${pares([
          ['Encomendas (30 dias)', formatNumero(m.encomendas_30d)],
          ['Volume (30 dias)', formatCents(m.volume_30d_cents)],
          ['Encomendas no total', formatNumero(m.encomendas)],
          ['Volume no total', formatCents(m.volume_cents)],
          ['Ticket médio', formatCents(m.ticket_medio_cents)],
          ['Última encomenda', desdeQuando(m.ultima_encomenda)],
          ['Por preparar agora', formatNumero(m.pendentes)],
          ['Canceladas no total', formatNumero(m.canceladas)],
        ])}
      </div>
    </div>

    <div class="p-secao">
      <h3>Conta</h3>
      <div class="p-pares">
        ${pares([
          ['Cliente desde', formatDateTime(r.created_at)],
          ['NIF', r.nif || '—'],
          ['Endereço próprio', r.slug],
          ['Domínio', r.domain || 'não configurado'],
          ['Produtos', `${formatNumero(m.produtos_disponiveis)} de ${formatNumero(m.produtos)} disponíveis`],
          ['IVA por omissão', `${(Number(r.taxa_iva_omissao_bp) / 100).toFixed(2).replace('.', ',')} %`],
          ['Pagamentos aceitos', [r.dinheiro_ativo ? 'dinheiro' : '', r.cartao_ativo ? 'cartão' : '']
            .filter(Boolean).join(' e ') || 'nenhum'],
          ['Marca da plataforma', r.mostrar_marca_plataforma ? 'visível' : 'oculta'],
        ])}
      </div>
    </div>

    <div class="p-secao">
      <h3>Equipa</h3>
      <div class="p-tabela-envolvente">
        <table class="p-tabela">
          <thead><tr><th>Pessoa</th><th>Perfil</th><th>Último acesso</th></tr></thead>
          <tbody>${equipa}</tbody>
        </table>
      </div>
    </div>

    <div class="p-secao">
      <h3>Últimas encomendas</h3>
      <div class="p-tabela-envolvente">
        <table class="p-tabela">
          <thead><tr><th>Quando</th><th>Estado</th><th>Pagamento</th><th class="p-num">Total</th></tr></thead>
          <tbody>${encomendas}</tbody>
        </table>
      </div>
      <p class="p-celula-secundaria" style="margin-top:0.75rem;">
        Os dados dos consumidores não são mostrados aqui: pertencem ao restaurante.
      </p>
    </div>

    <div class="p-secao">
      <h3>Suporte</h3>
      <div class="p-acoes">
        <a class="btn btn-secondary" href="https://${escAttr(endereco)}/menu"
           target="_blank" rel="noopener">Abrir o menu público</a>
        <button type="button" class="btn btn-secondary" id="btn-recuperacao">
          Enviar link de recuperação
        </button>
        ${accaoEstado}
      </div>
      <p class="p-celula-secundaria" style="margin-top:0.75rem;">
        O link de recuperação vai para o email do proprietário, não para o seu.
      </p>
    </div>`;

  $('detalhe-fechar').addEventListener('click', fecharDetalhe);
  $('btn-recuperacao').addEventListener('click', () => enviarRecuperacao(r.id));

  const suspender = $('btn-suspender');
  if (suspender) suspender.addEventListener('click', () => alterarEstado(r, false));
  const reactivar = $('btn-reactivar');
  if (reactivar) reactivar.addEventListener('click', () => alterarEstado(r, true));
}

async function alterarEstado(r, ativo) {
  let motivo = '';
  if (!ativo) {
    // Confirmação obrigatória: suspender fecha o menu ao público e tira a equipa do painel.
    motivo = window.prompt(
      `Suspender "${r.nome}"?\n\n` +
        'O menu deixa de responder no endereço público e a equipa não consegue entrar.\n' +
        'É reversível.\n\nMotivo (fica no registo de auditoria):',
      'Subscrição em atraso'
    );
    if (motivo === null) return;
  }

  try {
    const d = await apiP(`/api/plataforma/restaurantes/${encodeURIComponent(r.id)}/estado`, {
      metodo: 'PATCH',
      corpo: { ativo, motivo },
    });
    showToast(d.message, 'success');
    await abrirDetalhe(r.id);
    if (estado.vista === 'restaurantes') carregarRestaurantes();
  } catch (err) {
    if (err.status !== 401) showToast(err.message, 'error');
  }
}

async function enviarRecuperacao(id) {
  if (!window.confirm('Enviar um link de recuperação de senha para o proprietário deste restaurante?')) {
    return;
  }
  try {
    const d = await apiP(`/api/plataforma/restaurantes/${encodeURIComponent(id)}/recuperacao`, {
      metodo: 'POST',
      corpo: {},
    });
    showToast(d.message, 'success');
  } catch (err) {
    if (err.status !== 401) showToast(err.message, 'error');
  }
}

// --- Entrada e conta ---

async function entrar(e) {
  e.preventDefault();
  const btn = $('login-btn');
  btn.disabled = true;
  btn.textContent = 'A entrar…';

  try {
    const dados = await apiP('/api/plataforma/login', {
      metodo: 'POST',
      autenticado: false,
      corpo: {
        identifier: $('login-email').value.trim(),
        password: $('login-password').value,
      },
    });
    SessaoPlataforma.guardar(dados);
    $('login-password').value = '';
    mostrarConsola();
    mostrarVista('resumo');
  } catch (err) {
    showToast(err.message, 'error');
  } finally {
    btn.disabled = false;
    btn.textContent = 'Entrar';
  }
}

async function sair() {
  const refresh = SessaoPlataforma.refreshToken();
  if (refresh) {
    // Revogar do lado do servidor; se falhar, a sessão local é limpa de qualquer forma.
    await apiP('/api/plataforma/logout', {
      metodo: 'POST',
      autenticado: false,
      corpo: { refresh_token: refresh },
    }).catch(() => null);
  }
  SessaoPlataforma.limpar();
  mostrarEntrada();
}

async function guardarSenha(e) {
  e.preventDefault();
  try {
    const d = await apiP('/api/plataforma/conta/alterar-senha', {
      metodo: 'POST',
      corpo: {
        senha_atual: $('senha-atual').value,
        nova_senha: $('senha-nova').value,
      },
    });
    showToast(d.message, 'success');
    $('senha-fundo').hidden = true;
    $('senha-form').reset();
    // A alteração revoga as outras sessões, mas não esta: o token continua válido.
  } catch (err) {
    if (err.status !== 401) showToast(err.message, 'error');
  }
}

// --- Arranque ---

// atrasar agrupa as teclas de uma pesquisa num único pedido.
function atrasar(fn, ms) {
  let temporizador = null;
  return (...args) => {
    clearTimeout(temporizador);
    temporizador = setTimeout(() => fn(...args), ms);
  };
}

document.addEventListener('DOMContentLoaded', () => {
  $('login-form').addEventListener('submit', entrar);
  $('btn-sair').addEventListener('click', sair);

  $('nav').addEventListener('click', (e) => {
    const btn = e.target.closest('.p-nav-btn[data-vista]');
    if (btn) mostrarVista(btn.dataset.vista);
  });

  $('btn-actualizar-resumo').addEventListener('click', carregarResumo);
  $('btn-actualizar-auditoria').addEventListener('click', carregarAuditoria);
  $('btn-ver-todos').addEventListener('click', () => mostrarVista('restaurantes'));

  // Filtros da listagem. Voltar à primeira página é intencional: manter a página 7 ao
  // mudar de filtro mostra um ecrã vazio de um resultado que tem 2 páginas.
  const recarregarLista = () => {
    estado.lista.pagina = 1;
    carregarRestaurantes();
  };
  $('filtro-q').addEventListener('input', atrasar(recarregarLista, 350));
  $('filtro-estado').addEventListener('change', recarregarLista);
  $('filtro-ordem').addEventListener('change', recarregarLista);
  $('auditoria-acao').addEventListener(
    'input',
    atrasar(() => {
      estado.auditoria.pagina = 1;
      carregarAuditoria();
    }, 350)
  );

  // Delegação em vez de onclick por linha: a CSP não permite atributos de evento no HTML,
  // e as linhas são regeradas a cada pesquisa.
  const abrirDaLinha = (e) => {
    const tr = e.target.closest('tr[data-id]');
    if (tr) abrirDetalhe(tr.dataset.id);
  };
  $('lista-restaurantes').addEventListener('click', abrirDaLinha);
  $('lista-restaurantes').addEventListener('keydown', (e) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      abrirDaLinha(e);
    }
  });

  document.addEventListener('click', (e) => {
    const btn = e.target.closest('.p-btn-pagina[data-pagina]');
    if (!btn) return;
    const alvo = btn.dataset.tipo === 'auditoria' ? estado.auditoria : estado.lista;
    const paginas = Math.max(1, Math.ceil(alvo.total / alvo.porPagina));

    if (btn.dataset.pagina === 'anterior' && alvo.pagina > 1) alvo.pagina -= 1;
    if (btn.dataset.pagina === 'seguinte' && alvo.pagina < paginas) alvo.pagina += 1;

    if (btn.dataset.tipo === 'auditoria') carregarAuditoria();
    else carregarRestaurantes();
  });

  // Fechar a gaveta ao clicar fora dela.
  $('detalhe-fundo').addEventListener('click', (e) => {
    if (e.target === $('detalhe-fundo')) fecharDetalhe();
  });

  $('btn-alterar-senha').addEventListener('click', () => {
    $('senha-fundo').hidden = false;
    $('senha-atual').focus();
  });
  $('senha-cancelar').addEventListener('click', () => {
    $('senha-fundo').hidden = true;
    $('senha-form').reset();
  });
  $('senha-form').addEventListener('submit', guardarSenha);
  $('senha-fundo').addEventListener('click', (e) => {
    if (e.target === $('senha-fundo')) $('senha-fundo').hidden = true;
  });

  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape') return;
    if (!$('senha-fundo').hidden) $('senha-fundo').hidden = true;
    else if (!$('detalhe-fundo').hidden) fecharDetalhe();
  });

  // Sessão guardada: confirmar com o servidor antes de mostrar a consola. Um token
  // expirado no localStorage mostraria o interface e falharia todos os pedidos.
  if (SessaoPlataforma.temSessao()) {
    apiP('/api/plataforma/eu')
      .then((admin) => {
        SessaoPlataforma.guardar({
          access_token: SessaoPlataforma.accessToken(),
          refresh_token: SessaoPlataforma.refreshToken(),
          admin,
        });
        mostrarConsola();
        mostrarVista('resumo');
      })
      .catch(() => mostrarEntrada());
    return;
  }
  mostrarEntrada();
});
