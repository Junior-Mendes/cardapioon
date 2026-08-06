// Configuração de lint do frontend, com um objectivo principal: apanhar identificadores
// que não existem.
//
// Um erro desta classe chegou a produção. A função que submete a encomenda referia uma
// variável nunca declarada (resíduo de uma substituição parcial), o que lançava
// ReferenceError antes do envio: o botão ficava preso em "A enviar..." e nenhum pedido
// chegava ao servidor. O `node --check` não o apanha, porque a sintaxe era válida.

// Funções e objectos partilhados entre ficheiros. Os scripts são carregados como scripts
// clássicos, pelo que comunicam por globais.
const partilhados = {
  // common.js
  esc: 'readonly',
  escAttr: 'readonly',
  formatCurrency: 'readonly',
  formatCents: 'readonly',
  formatDateTime: 'readonly',
  formatTime: 'readonly',
  ivaIncluido: 'readonly',
  parseValor: 'readonly',
  showToast: 'readonly',
  Sessao: 'readonly',
  SegGroup: 'readonly',
  api: 'readonly',
  uuid: 'readonly',
  ErroAPI: 'readonly',
  // branding.js
  aplicarBranding: 'readonly',
  // alertas.js
  Som: 'readonly',
  Titulo: 'readonly',
  Eventos: 'readonly',
  registarServiceWorker: 'readonly',
  prepararInstalacao: 'readonly',
  instalarApp: 'readonly',
  inicializarSubscricaoPush: 'readonly',
};

module.exports = {
  root: true,
  env: { browser: true, es2022: true },
  parserOptions: { ecmaVersion: 2022, sourceType: 'script' },

  rules: {
    // A regra que importa: identificadores inexistentes.
    'no-undef': 'error',

    // Erros que passam por sintaxe válida.
    'no-redeclare': 'error',
    'no-dupe-keys': 'error',
    'no-dupe-args': 'error',
    'no-unreachable': 'error',
    'no-const-assign': 'error',
    'no-self-assign': 'error',
    'no-cond-assign': 'error',
    'no-fallthrough': 'error',
    'no-sparse-arrays': 'error',

    // Variáveis não usadas são aviso, não erro: são frequentemente resíduo de refactor e
    // vale a pena vê-las, mas não devem travar um deploy.
    'no-unused-vars': ['warn', { args: 'none', varsIgnorePattern: '^_' }],
  },

  overrides: [
    {
      // Ficheiros de página, que CONSOMEM a API partilhada.
      //
      // Os globais são declarados só aqui, e não na configuração base: em common.js e
      // branding.js, que os DEFINEM, declará-los faria o lint acusar redeclaração.
      files: ['admin.js', 'menu.js', 'order.js', 'landing.js', 'reset.js'],
      globals: partilhados,
    },
    {
      // plataforma.js é a consola do dono do SaaS. Consome só os utilitários de common.js:
      // tem sessão e cliente de API próprios, porque partilhar as chaves de localStorage
      // com o painel do lojista faria uma sessão substituir a outra no mesmo browser.
      files: ['plataforma.js'],
      globals: {
        esc: 'readonly',
        escAttr: 'readonly',
        formatCents: 'readonly',
        formatDateTime: 'readonly',
        showToast: 'readonly',
      },
    },
    {
      // common.js define toda a API partilhada e não consome nada dela, pelo que não
      // declara globais. As suas funções parecem não usadas porque só são chamadas de
      // outros ficheiros; só as variáveis locais são verificadas.
      files: ['common.js'],
      rules: {
        'no-unused-vars': ['warn', { args: 'none', vars: 'local' }],
      },
    },
    {
      // alertas.js define Som, Titulo e Eventos, e consome Sessao e api de common.js.
      files: ['alertas.js'],
      globals: {
        Sessao: 'readonly',
        api: 'readonly',
        showToast: 'readonly',
      },
      rules: {
        'no-unused-vars': ['warn', { args: 'none', vars: 'local' }],
      },
    },
    {
      // branding.js define aplicarBranding e consome o escape de common.js. Os globais
      // são declarados um a um, e não em bloco, para que no-redeclare continue activo e
      // apanhe uma definição duplicada acidental.
      files: ['branding.js'],
      globals: {
        esc: 'readonly',
        escAttr: 'readonly',
      },
      rules: {
        'no-unused-vars': ['warn', { args: 'none', vars: 'local' }],
      },
    },
  ],
};
