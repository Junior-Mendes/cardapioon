// Módulo partilhado: escape de HTML, formatação e cliente de API.
//
// Os três ficheiros de página duplicavam formatação de moeda e construíam HTML por
// interpolação directa de dados do servidor, o que produzia XSS armazenado: o nome de um
// produto ou de um cliente com <script> executava no painel do lojista, e como o token
// vive em localStorage isso equivale a tomada de conta.

'use strict';

// --- Escape ---

const MAPA_ESCAPE = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
  '`': '&#96;',
};

/**
 * esc escapa texto para interpolação segura em HTML.
 * Usar SEMPRE que um valor vindo do servidor entra num template de innerHTML.
 * @param {*} valor
 * @returns {string}
 */
function esc(valor) {
  if (valor === null || valor === undefined) return '';
  return String(valor).replace(/[&<>"'`]/g, (ch) => MAPA_ESCAPE[ch]);
}

/**
 * escAttr escapa um valor para uso dentro de um atributo HTML entre aspas.
 * Além do escape de esc(), remove parênteses para que o valor não possa fechar uma
 * chamada de função em atributos de evento gerados dinamicamente.
 */
function escAttr(valor) {
  return esc(valor).replace(/[()]/g, '');
}

// --- Formatação (pt-PT / EUR) ---

const FORMATADOR_MOEDA = new Intl.NumberFormat('pt-PT', {
  style: 'currency',
  currency: 'EUR',
});

/** formatCurrency devolve o valor em euros no formato português (1 234,56 €). */
function formatCurrency(valor) {
  const n = Number(valor);
  return FORMATADOR_MOEDA.format(Number.isFinite(n) ? n : 0);
}

/**
 * formatCents formata um valor em cêntimos: 1250 -> "12,50 €".
 * Preferir sempre a esta função em vez de formatCurrency com euros em float.
 */
function formatCents(cents) {
  const n = Number(cents) || 0;
  return FORMATADOR_MOEDA.format(n / 100);
}

/**
 * ivaIncluido extrai o IVA contido num valor que já inclui imposto.
 *
 * Espelha exactamente a função do servidor, incluindo o arredondamento meio-para-cima em
 * aritmética inteira. Serve apenas para pré-visualização no painel: o valor que conta é
 * sempre o que o servidor calcula.
 *
 * @param {number} brutoCents  valor com IVA incluído, em cêntimos
 * @param {number} taxaBP      taxa em pontos base (2300 = 23%)
 */
function ivaIncluido(brutoCents, taxaBP) {
  const bruto = Math.abs(Math.round(Number(brutoCents) || 0));
  const taxa = Math.round(Number(taxaBP) || 0);
  if (taxa <= 0 || bruto === 0) return 0;

  const denominador = 10000 + taxa;
  return Math.floor((bruto * taxa + Math.floor(denominador / 2)) / denominador);
}

/** parseValor converte "12,50" ou "12.5" em cêntimos, ou devolve null se inválido. */
function parseValor(texto) {
  let s = String(texto ?? '').trim().replace(/€/g, '').replace(/\s/g, '');
  if (s === '') return null;
  s = s.replace(',', '.');
  if (!/^\d*(\.\d{0,2})?$/.test(s)) return null;

  const [inteiros = '0', decimais = ''] = s.split('.');
  const dec = (decimais + '00').slice(0, 2);
  return Number(inteiros || '0') * 100 + Number(dec);
}

const FORMATADOR_DATA = new Intl.DateTimeFormat('pt-PT', {
  day: '2-digit',
  month: '2-digit',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
});

function formatDateTime(valor) {
  if (!valor) return '—';
  const d = new Date(valor);
  return Number.isNaN(d.getTime()) ? '—' : FORMATADOR_DATA.format(d);
}

function formatTime(valor) {
  if (!valor) return '—';
  const d = new Date(valor);
  if (Number.isNaN(d.getTime())) return '—';
  return d.toLocaleTimeString('pt-PT', { hour: '2-digit', minute: '2-digit' });
}

// --- Selector segmentado ---

/**
 * SegGroup opera um grupo de botões com role="radiogroup".
 *
 * Substitui um <select> quando as opções são poucas e fixas. O popup nativo de um select
 * não herda o fundo do tema — usa o do sistema — o que num tema escuro deixava o texto das
 * opções quase invisível. Além disso mostra todas as opções de uma vez e evita um toque
 * extra no telemóvel.
 */
const SegGroup = {
  /** valor devolve a opção seleccionada, como string. */
  valor(id) {
    const grupo = document.getElementById(id);
    if (!grupo) return '';
    const activa = grupo.querySelector('[aria-checked="true"]');
    return activa ? activa.dataset.valor : '';
  },

  /** definir selecciona a opção com o valor indicado. */
  definir(id, valor) {
    const grupo = document.getElementById(id);
    if (!grupo) return;

    const opcoes = [...grupo.querySelectorAll('[role="radio"]')];
    let encontrada = false;

    opcoes.forEach((o) => {
      const seleccionada = String(o.dataset.valor) === String(valor);
      o.setAttribute('aria-checked', seleccionada ? 'true' : 'false');
      // Só a opção activa entra na ordem de tabulação, como manda o padrão de radiogroup.
      o.tabIndex = seleccionada ? 0 : -1;
      if (seleccionada) encontrada = true;
    });

    // Valor desconhecido (por exemplo uma taxa gravada que já não está na lista):
    // selecciona a primeira para não deixar o grupo sem escolha.
    if (!encontrada && opcoes.length > 0) {
      opcoes[0].setAttribute('aria-checked', 'true');
      opcoes[0].tabIndex = 0;
    }
  },

  /**
   * ligar activa o grupo e chama onChange a cada alteração.
   * Suporta rato, toque e setas do teclado.
   */
  ligar(id, onChange) {
    const grupo = document.getElementById(id);
    if (!grupo || grupo.dataset.ligado) return;
    grupo.dataset.ligado = '1';

    const opcoes = [...grupo.querySelectorAll('[role="radio"]')];

    const seleccionar = (opcao) => {
      this.definir(id, opcao.dataset.valor);
      opcao.focus();
      if (onChange) onChange(opcao.dataset.valor);
    };

    opcoes.forEach((opcao, indice) => {
      opcao.addEventListener('click', () => seleccionar(opcao));

      opcao.addEventListener('keydown', (e) => {
        let destino = null;
        switch (e.key) {
          case 'ArrowRight':
          case 'ArrowDown':
            destino = opcoes[(indice + 1) % opcoes.length];
            break;
          case 'ArrowLeft':
          case 'ArrowUp':
            destino = opcoes[(indice - 1 + opcoes.length) % opcoes.length];
            break;
          case ' ':
          case 'Enter':
            destino = opcao;
            break;
          default:
            return;
        }
        e.preventDefault();
        seleccionar(destino);
      });
    });

    // Garante um tabIndex coerente à partida.
    this.definir(id, this.valor(id));
  },
};

// --- Toast ---

function showToast(mensagem, tipo = 'info') {
  const container = document.getElementById('toast-container');
  if (!container) return;

  const toast = document.createElement('div');
  toast.className = `toast toast-${tipo} glass`;

  // textContent em vez de innerHTML: uma mensagem de erro do servidor nunca deve poder
  // injectar marcação.
  const span = document.createElement('span');
  span.textContent = mensagem;
  toast.appendChild(span);

  container.appendChild(toast);
  setTimeout(() => toast.classList.add('show'), 50);
  setTimeout(() => {
    toast.classList.remove('show');
    setTimeout(() => toast.remove(), 300);
  }, 3500);
}

// --- Sessão ---

const CHAVE_ACCESS = 'cardapio_access_token';
const CHAVE_REFRESH = 'cardapio_refresh_token';
const CHAVE_SESSAO = 'cardapio_sessao';

const Sessao = {
  guardar(dados) {
    localStorage.setItem(CHAVE_ACCESS, dados.access_token);
    localStorage.setItem(CHAVE_REFRESH, dados.refresh_token);
    localStorage.setItem(
      CHAVE_SESSAO,
      JSON.stringify({
        nome: dados.usuario?.nome || '',
        email: dados.usuario?.email || '',
        role: dados.usuario?.role || '',
        slug: dados.restaurante?.slug || '',
        restaurante: dados.restaurante?.nome || '',
      })
    );
  },

  accessToken() {
    return localStorage.getItem(CHAVE_ACCESS);
  },

  refreshToken() {
    return localStorage.getItem(CHAVE_REFRESH);
  },

  info() {
    try {
      return JSON.parse(localStorage.getItem(CHAVE_SESSAO) || '{}');
    } catch {
      return {};
    }
  },

  temSessao() {
    return Boolean(this.accessToken());
  },

  limpar() {
    localStorage.removeItem(CHAVE_ACCESS);
    localStorage.removeItem(CHAVE_REFRESH);
    localStorage.removeItem(CHAVE_SESSAO);
    // Chaves da versão anterior, para que um utilizador com sessão antiga não fique
    // preso num estado inconsistente.
    localStorage.removeItem('admin_token');
    localStorage.removeItem('admin_name');
    localStorage.removeItem('admin_slug');
  },
};

// --- Cliente de API ---

class ErroAPI extends Error {
  constructor(mensagem, status) {
    super(mensagem);
    this.status = status;
  }
}

let renovacaoEmCurso = null;

/**
 * renovarSessao troca o refresh token por um novo par.
 * Várias chamadas concorrentes partilham a mesma promessa para não gastar o token duas
 * vezes — o backend revoga um refresh token reutilizado, o que expulsaria o utilizador.
 */
async function renovarSessao() {
  if (renovacaoEmCurso) return renovacaoEmCurso;

  const refresh = Sessao.refreshToken();
  if (!refresh) return null;

  renovacaoEmCurso = (async () => {
    try {
      const res = await fetch('/api/tenant/refresh', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: refresh }),
      });
      if (!res.ok) return null;

      const dados = await res.json();
      localStorage.setItem(CHAVE_ACCESS, dados.access_token);
      localStorage.setItem(CHAVE_REFRESH, dados.refresh_token);
      return dados.access_token;
    } catch {
      return null;
    } finally {
      renovacaoEmCurso = null;
    }
  })();

  return renovacaoEmCurso;
}

/**
 * api faz um pedido autenticado, renovando a sessão automaticamente num 401.
 * @param {string} caminho
 * @param {object} opcoes  { metodo, corpo, autenticado, idempotencyKey }
 */
async function api(caminho, opcoes = {}) {
  const {
    metodo = 'GET',
    corpo = null,
    autenticado = true,
    idempotencyKey = null,
  } = opcoes;

  const executar = async () => {
    const headers = {};
    if (corpo !== null) headers['Content-Type'] = 'application/json';
    if (autenticado) {
      const tok = Sessao.accessToken();
      if (tok) headers['Authorization'] = `Bearer ${tok}`;
    }
    if (idempotencyKey) headers['Idempotency-Key'] = idempotencyKey;

    return fetch(caminho, {
      method: metodo,
      headers,
      body: corpo !== null ? JSON.stringify(corpo) : undefined,
    });
  };

  let res = await executar();

  // Sessão expirada: tentar renovar uma vez e repetir.
  if (res.status === 401 && autenticado && Sessao.refreshToken()) {
    const novo = await renovarSessao();
    if (novo) {
      res = await executar();
    }
  }

  if (res.status === 401 && autenticado) {
    Sessao.limpar();
    throw new ErroAPI('A sessão terminou. Entre novamente.', 401);
  }

  let dados = null;
  const tipo = res.headers.get('Content-Type') || '';
  if (tipo.includes('application/json')) {
    dados = await res.json().catch(() => null);
  }

  if (!res.ok) {
    throw new ErroAPI(dados?.error || 'Não foi possível concluir a operação.', res.status);
  }
  return dados;
}

/** uuid gera uma chave de idempotência para o checkout. */
function uuid() {
  if (window.crypto?.randomUUID) return window.crypto.randomUUID();
  // Fallback para contextos sem randomUUID (http em desenvolvimento).
  const bytes = new Uint8Array(16);
  window.crypto.getRandomValues(bytes);
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = [...bytes].map((b) => b.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
