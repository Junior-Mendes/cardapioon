// Aviso de encomenda nova no painel: som, título do separador e ligação em tempo real.
//
// O painel sondava o servidor a cada quinze segundos, em silêncio. Se o lojista não
// estivesse a olhar para o ecrã, não sabia que tinha encomenda — e ao balcão ninguém olha
// para um ecrã de forma contínua.

'use strict';

// --- Som ---
//
// O tom é gerado com a Web Audio API em vez de carregar um ficheiro. Três razões: não há
// asset para falhar a carregar, funciona sem rede depois da página estar aberta, e o padrão
// (duração, repetições, frequência) fica sob controlo para se distinguir de qualquer outro
// som do telemóvel.

const CHAVE_SOM = 'cardapio_som_activo';

const Som = {
  contexto: null,
  // armado indica que houve um gesto do utilizador e o browser deixa tocar.
  //
  // Isto não é um detalhe: os browsers bloqueiam áudio até haver interação, e um painel que
  // o lojista acredita estar a avisar mas está mudo é pior do que não ter som nenhum. Por
  // isso o estado é explícito e mostrado na interface.
  armado: false,

  // preferido é o que o lojista escolheu, e sobrevive a um refresh. Não implica armado:
  // depois de recarregar a página é preciso um novo gesto.
  preferido() {
    return localStorage.getItem(CHAVE_SOM) !== 'off';
  },

  definirPreferido(activo) {
    localStorage.setItem(CHAVE_SOM, activo ? 'on' : 'off');
  },

  /**
   * armar cria o contexto de áudio. Tem de ser chamado de dentro de um handler de gesto
   * (clique, toque), ou o browser recusa.
   * @returns {boolean} se ficou pronto a tocar
   */
  armar() {
    try {
      if (!this.contexto) {
        const AudioCtx = window.AudioContext || window.webkitAudioContext;
        if (!AudioCtx) return false;
        this.contexto = new AudioCtx();
      }
      // Um contexto criado antes do gesto fica suspenso; resume() precisa do gesto.
      if (this.contexto.state === 'suspended') this.contexto.resume();
      this.armado = this.contexto.state === 'running';
      return this.armado;
    } catch {
      return false;
    }
  },

  /** tocar emite o padrão de aviso. Silencioso se não estiver armado ou desligado. */
  tocar({ repeticoes = 3 } = {}) {
    if (!this.armado || !this.preferido() || !this.contexto) return;

    const agora = this.contexto.currentTime;
    for (let i = 0; i < repeticoes; i++) {
      // Duas notas por repetição, a subir: um padrão de dois tons distingue-se de
      // notificações de sistema, que costumam ser um só toque.
      this.nota(agora + i * 0.5, 880, 0.12);
      this.nota(agora + i * 0.5 + 0.15, 1170, 0.16);
    }
  },

  nota(quando, frequencia, duracao) {
    const osc = this.contexto.createOscillator();
    const ganho = this.contexto.createGain();

    osc.type = 'sine';
    osc.frequency.value = frequencia;

    // Envelope: sem ele, ligar e desligar o oscilador produz um estalido audível.
    ganho.gain.setValueAtTime(0, quando);
    ganho.gain.linearRampToValueAtTime(0.28, quando + 0.02);
    ganho.gain.exponentialRampToValueAtTime(0.0001, quando + duracao);

    osc.connect(ganho);
    ganho.connect(this.contexto.destination);
    osc.start(quando);
    osc.stop(quando + duracao + 0.02);
  },
};

// --- Aviso visual no separador ---
//
// O som pode não bastar numa cozinha com ruído, e o lojista pode ter o painel num
// separador de fundo. O título pisca até a página voltar a ter foco.

const Titulo = {
  original: document.title,
  timer: null,
  pendentes: 0,

  actualizar(pendentes) {
    this.pendentes = pendentes;

    if (pendentes <= 0) {
      this.parar();
      return;
    }

    document.title = `(${pendentes}) ${this.original}`;

    // Piscar só faz sentido quando a página não está visível: com o lojista a olhar, o
    // título a mudar é ruído.
    if (document.visibilityState === 'visible') {
      this.pararPisca();
      return;
    }
    this.comecarPisca();
  },

  comecarPisca() {
    if (this.timer) return;
    let alternado = false;
    this.timer = setInterval(() => {
      alternado = !alternado;
      document.title = alternado
        ? `🔔 ${this.pendentes} encomenda${this.pendentes === 1 ? '' : 's'}!`
        : `(${this.pendentes}) ${this.original}`;
    }, 1200);
  },

  pararPisca() {
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }
    if (this.pendentes > 0) {
      document.title = `(${this.pendentes}) ${this.original}`;
    }
  },

  parar() {
    this.pararPisca();
    document.title = this.original;
  },
};

document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') Titulo.pararPisca();
  else if (Titulo.pendentes > 0) Titulo.comecarPisca();
});

// --- Ligação de eventos em tempo real ---
//
// Usa fetch com ReadableStream e não EventSource, porque a API EventSource do browser não
// permite definir cabeçalhos e o token de sessão vai no Authorization. A alternativa seria
// passá-lo na query string, onde ficaria em registos de acesso de qualquer intermediário.

const Eventos = {
  controlador: null,
  aTentar: false,
  tentativas: 0,
  handlers: {},

  /** ligar abre o stream e chama os handlers registados. */
  ligar() {
    if (this.aTentar || !Sessao.temSessao()) return;
    this.aTentar = true;
    this.abrir();
  },

  desligar() {
    this.aTentar = false;
    if (this.controlador) {
      this.controlador.abort();
      this.controlador = null;
    }
  },

  em(tipo, handler) {
    this.handlers[tipo] = handler;
  },

  async abrir() {
    if (!this.aTentar) return;

    this.controlador = new AbortController();

    try {
      const res = await fetch('/api/admin/eventos', {
        headers: { Authorization: `Bearer ${Sessao.accessToken()}` },
        signal: this.controlador.signal,
      });

      if (res.status === 401) {
        // O token expirou. Uma chamada normal renova a sessão por dentro de api(); depois
        // disso vale a pena tentar de novo.
        try {
          await api('/api/admin/config');
        } catch {
          this.desligar();
          return;
        }
        return this.reagendar();
      }
      if (!res.ok || !res.body) return this.reagendar();

      this.tentativas = 0;
      this.anunciarEstado(true);

      const leitor = res.body.getReader();
      const descodificador = new TextDecoder();
      let restante = '';

      // Loop de leitura. Os eventos SSE são separados por linha em branco, e um chunk da
      // rede pode partir um evento a meio — daí acumular em `restante`.
      for (;;) {
        const { done, value } = await leitor.read();
        if (done) break;

        restante += descodificador.decode(value, { stream: true });

        let corte;
        while ((corte = restante.indexOf('\n\n')) >= 0) {
          const bruto = restante.slice(0, corte);
          restante = restante.slice(corte + 2);
          this.processar(bruto);
        }
      }
    } catch (err) {
      // AbortError é a saída normal quando desligamos de propósito.
      if (err.name !== 'AbortError') {
        console.warn('stream de eventos interrompido:', err.message);
      }
    }

    this.anunciarEstado(false);
    this.reagendar();
  },

  processar(bruto) {
    // Comentários (linhas iniciadas por ':') são manutenção da ligação.
    if (bruto.startsWith(':')) return;

    let tipo = 'message';
    let dados = '';
    bruto.split('\n').forEach((linha) => {
      if (linha.startsWith('event:')) tipo = linha.slice(6).trim();
      else if (linha.startsWith('data:')) dados += linha.slice(5).trim();
    });

    if (!dados) return;
    let evento;
    try {
      evento = JSON.parse(dados);
    } catch {
      return;
    }

    const handler = this.handlers[tipo];
    if (handler) handler(evento);
  },

  /** reagendar volta a ligar com espera crescente, até 30 segundos. */
  reagendar() {
    if (!this.aTentar) return;

    this.tentativas++;
    // Crescimento exponencial travado: com o servidor em baixo, cem painéis a tentar de
    // segundo a segundo agravariam o problema.
    const espera = Math.min(30000, 1000 * 2 ** Math.min(this.tentativas, 5));
    setTimeout(() => this.abrir(), espera);
  },

  anunciarEstado(ligado) {
    if (this.handlers.__estado) this.handlers.__estado(ligado);
  },
};

// --- Instalação como aplicação ---
//
// Registar o service worker é o que torna o painel instalável, e o que mais tarde permite
// notificações mesmo com o painel fechado. No iPhone é a única via: o sistema só entrega
// notificações a sites instalados no ecrã principal.

function registarServiceWorker() {
  if (!('serviceWorker' in navigator)) return;

  // O caminho '/sw.js' e o scope '/' são deliberados: um service worker só controla
  // páginas dentro do seu próprio caminho, e servido de /static/ não controlaria /admin.
  navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch((err) => {
    // Falhar aqui não impede o painel de funcionar; perde-se a instalação e o modo offline.
    console.warn('service worker não registado:', err.message);
  });
}

// O evento é guardado porque só pode ser usado mais tarde, a partir de um gesto do
// utilizador — chamar prompt() fora de um clique é ignorado pelo browser.
let promptInstalacao = null;

function prepararInstalacao() {
  window.addEventListener('beforeinstallprompt', (e) => {
    // Impede a barra automática do browser, para oferecer a instalação no momento certo.
    e.preventDefault();
    promptInstalacao = e;
    mostrarBotaoInstalar(true);
  });

  window.addEventListener('appinstalled', () => {
    promptInstalacao = null;
    mostrarBotaoInstalar(false);
  });

  // Já instalado: não faz sentido oferecer.
  if (window.matchMedia('(display-mode: standalone)').matches) {
    mostrarBotaoInstalar(false);
  }
}

function mostrarBotaoInstalar(mostrar) {
  const btn = document.getElementById('btn-instalar');
  if (btn) btn.hidden = !mostrar;
}

async function instalarApp() {
  // O Safari em iOS não implementa beforeinstallprompt: a instalação é manual, pelo menu
  // de partilha. Explicar é melhor do que um botão que não faz nada.
  if (!promptInstalacao) {
    const ios = /iPad|iPhone|iPod/.test(navigator.userAgent);
    showToast(
      ios
        ? 'No iPhone: toque em Partilhar e escolha "Adicionar ao ecrã principal".'
        : 'Use o menu do navegador e escolha "Instalar aplicação".',
      'info'
    );
    return;
  }

  promptInstalacao.prompt();
  const { outcome } = await promptInstalacao.userChoice;
  promptInstalacao = null;
  mostrarBotaoInstalar(false);

  if (outcome === 'accepted') {
    showToast('Painel instalado. Pode abri-lo a partir do ecrã principal.', 'success');
  }
}
