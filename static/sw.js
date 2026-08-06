// Service worker do painel.
//
// Duas funções, e nenhuma delas é cache agressiva:
//
//   1. Tornar o painel instalável, para viver em ecrã completo num tablet ao balcão. É
//      também o pré-requisito das notificações no iPhone, que só as permite a partir de uma
//      aplicação instalada no ecrã principal.
//   2. Dar uma página de recurso quando falta rede, em vez do erro do browser.
//
// A cache é deliberadamente conservadora. Um service worker que sirva JavaScript antigo
// depois de um deploy é uma das formas mais frustrantes de avaria: o painel deixa de
// funcionar e recarregar não resolve. Por isso o código e as páginas vão sempre à rede
// primeiro, e só cai para a cache se a rede falhar.

'use strict';

// A versão entra no nome da cache. Ao mudar, o novo worker apaga as caches antigas — é o
// que evita servir uma mistura de duas versões.
const VERSAO = 'v1';
const CACHE = `painel-${VERSAO}`;

// Apenas o indispensável para mostrar algo sem rede.
const ESSENCIAIS = [
  '/admin',
  '/static/css/style.css',
  '/static/css/admin.css',
  '/static/js/common.js',
  '/static/js/alertas.js',
  '/static/js/admin.js',
  '/static/icons/icone-192.png',
];

self.addEventListener('install', (evento) => {
  evento.waitUntil(
    caches
      .open(CACHE)
      // addAll falha inteiro se um recurso falhar; individualmente, um 404 não impede a
      // instalação do resto.
      .then((cache) => Promise.allSettled(ESSENCIAIS.map((u) => cache.add(u))))
      // Assume o controlo sem esperar que todos os separadores fechem: numa instalação
      // nova não há nada em uso que se possa quebrar.
      .then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', (evento) => {
  evento.waitUntil(
    caches
      .keys()
      .then((nomes) =>
        Promise.all(nomes.filter((n) => n !== CACHE).map((n) => caches.delete(n)))
      )
      .then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (evento) => {
  const pedido = evento.request;

  // Só GET é interceptado: um POST guardado em cache seria um erro grave.
  if (pedido.method !== 'GET') return;

  const url = new URL(pedido.url);

  // Nada de outros domínios.
  if (url.origin !== self.location.origin) return;

  // A API nunca é cacheada, nem servida de cache.
  //
  // São dados por restaurante e por sessão, e uma lista de encomendas antiga é pior do que
  // nenhuma: o lojista veria encomendas já preparadas como pendentes.
  if (url.pathname.startsWith('/api/')) return;

  // Rede primeiro para tudo o resto, com a cache apenas como recurso.
  evento.respondWith(
    fetch(pedido)
      .then((resposta) => {
        // Só respostas completas e válidas entram na cache.
        if (resposta.ok && resposta.status === 200) {
          const copia = resposta.clone();
          caches.open(CACHE).then((cache) => cache.put(pedido, copia));
        }
        return resposta;
      })
      .catch(async () => {
        const emCache = await caches.match(pedido);
        if (emCache) return emCache;

        // Sem rede e sem cache: para navegação, o painel guardado; para o resto, deixa
        // falhar, que é mais honesto do que devolver algo errado.
        if (pedido.mode === 'navigate') {
          const painel = await caches.match('/admin');
          if (painel) return painel;
        }
        return new Response('Sem ligação à internet.', {
          status: 503,
          headers: { 'Content-Type': 'text/plain; charset=utf-8' },
        });
      })
  );
});

// --- Web Push ---

self.addEventListener('push', (evento) => {
  let dados = {};
  try {
    dados = evento.data.json();
  } catch (err) {
    dados = {
      title: 'Nova Encomenda!',
      body: evento.data ? evento.data.text() : 'Chegou uma nova encomenda!',
    };
  }

  const opcoes = {
    body: dados.body,
    icon: '/static/icons/icone-192.png',
    badge: '/static/icons/icone-192.png',
    tag: dados.tag || 'nova-encomenda',
    renotify: true,
    data: dados.data || {},
  };

  evento.waitUntil(
    self.registration.showNotification(dados.title, opcoes)
  );
});

self.addEventListener('notificationclick', (evento) => {
  evento.notification.close();

  const url = (evento.notification.data && evento.notification.data.url) || '/admin';

  evento.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clientes) => {
      // Se houver uma aba aberta, foca nela e navega para a URL
      for (const cliente of clientes) {
        const clienteUrl = new URL(cliente.url);
        if (clienteUrl.pathname === url || clienteUrl.pathname === '/admin') {
          return cliente.focus().then((c) => {
            if (c.url !== clienteUrl.origin + url) {
              return c.navigate(url);
            }
          });
        }
      }
      // Se não houver, abre uma nova aba
      if (self.clients.openWindow) {
        return self.clients.openWindow(url);
      }
    })
  );
});
