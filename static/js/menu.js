// Menu público e checkout do cliente.
//
// Depende de common.js (esc, api, formatCurrency, showToast, uuid).
//
// Âmbito do MVP: levantamento ao balcão com pagamento na caixa. Não há entrega nem
// pagamento na aplicação, pelo que o simulador de Pix e o formulário de cartão de crédito
// da versão anterior foram removidos — ambos davam ao cliente a impressão de ter pago
// quando nenhum pagamento acontecia.

'use strict';

let menuData = null;
let cart = [];
// Termo de pesquisa activo. Vazio mostra o menu completo.
let termoBusca = '';
// Prato aberto no modal de detalhe, e a quantidade escolhida lá.
let itemAberto = null;
let qtdAberta = 1;
// Observador que destaca a categoria da secção visível.
let observadorSeccoes = null;
let currentStep = 1;

// Chave de idempotência da encomenda em curso.
//
// Gerada ao abrir o checkout e reutilizada em cada tentativa de submissão, para que um
// duplo-toque ou um retry após timeout não crie duas encomendas.
let chaveIdempotencia = null;

// Slug do restaurante, quando indicado explicitamente (ex.: ?tenant=tasca-do-bairro).
// No uso normal o restaurante é resolvido pelo subdomínio ou domínio próprio.
const urlParams = new URLSearchParams(window.location.search);
let tenantSlug = urlParams.get('tenant');
let resolvedByDomain = false;

document.addEventListener('DOMContentLoaded', () => {
  detectAndLoadMenu();
  setupEventListeners();
});

async function detectAndLoadMenu() {
  try {
    const detectRes = await fetch('/api/tenant/detect');
    if (detectRes.ok) {
      const detectData = await detectRes.json();
      if (detectData.status === 'resolved' && detectData.tenant) {
        tenantSlug = detectData.tenant.slug;
        resolvedByDomain = true;
      }
    }
  } catch (err) {
    console.warn("Erro ao detectar restaurante por domínio:", err);
  }

  // Sem restaurante resolvido não há menu para mostrar. A versão anterior tinha aqui um
  // slug de demonstração fixo, o que levava a um 404 confuso assim que esse restaurante
  // deixasse de existir.
  if (!tenantSlug) {
    mostrarSemRestaurante();
    return;
  }

  loadRestaurantMenu();
}

// mostrarSemRestaurante explica o que fazer quando o endereço não identifica uma loja.
function mostrarSemRestaurante() {
  document.getElementById('menu-items-container').innerHTML = `
    <div class="cart-empty glass" style="grid-column: 1/-1;">
      <div class="cart-empty-icon">🔎</div>
      <h3>Nenhum restaurante neste endereço</h3>
      <p>Use o link que o restaurante lhe deu, ou visite a página inicial para conhecer o serviço.</p>
      <a href="/" class="btn btn-secondary" style="margin-top:1rem;">Ir para a página inicial</a>
    </div>
  `;
  const barra = document.getElementById('mobile-cart-bar');
  if (barra) barra.classList.remove('active');
}

// Busca o cardápio público no back-end
async function loadRestaurantMenu() {
  try {
    const url = resolvedByDomain ? '/api/public-menu' : `/api/${tenantSlug}/public-menu`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('Não foi possível carregar o cardápio deste restaurante.');
    
    menuData = await res.json();
    // O branding é aplicado antes de renderizar, para que o cliente não veja um instante
    // com as cores da plataforma antes de as do restaurante entrarem.
    aplicarBranding(menuData.restaurante);
    renderRestaurantHeader();
    renderCategories();
    renderProducts();
    updateCartUI();
  } catch (err) {
    showToast(err.message, 'error');
    document.getElementById('menu-items-container').innerHTML = `
      <div class="cart-empty glass" style="grid-column: 1/-1;">
        <div class="cart-empty-icon">⚠️</div>
        <h3>Restaurante não encontrado</h3>
        <p>Verifique o link ou tente novamente mais tarde.</p>
      </div>
    `;
  }
}

// Renderiza o cabeçalho do restaurante.
// O logótipo e as cores são tratados por branding.js.
function renderRestaurantHeader() {
  const rest = menuData.restaurante;
  document.getElementById('rest-name').innerText = rest.nome;
}

// categoriasDoMenu devolve as categorias na ordem em que aparecem, com os destaques à
// frente quando existem.
function categoriasDoMenu() {
  const cats = [];
  menuData.itens.forEach((i) => {
    if (!cats.includes(i.categoria)) cats.push(i.categoria);
  });
  return cats;
}

// idParaSeccao converte o nome de uma categoria num id de elemento estável.
//
// O nome vem do lojista e pode ter acentos, espaços ou pontuação; usá-lo directamente num
// id e num selector quebraria. O índice garante unicidade mesmo com nomes que normalizem
// para o mesmo texto.
function idParaSeccao(indice) {
  return `secao-${indice}`;
}

// renderCategories desenha a barra de navegação.
//
// Ao contrário da versão anterior, os separadores não filtram o conteúdo: todas as
// categorias são visíveis como secções e a barra leva o cliente até elas. É assim que
// funcionam as apps de comida, e evita que o cliente pense que o menu é só o que está
// visível.
function renderCategories() {
  const bar = document.getElementById('categories-bar');
  const cats = categoriasDoMenu();
  const temDestaques = (menuData.destaques || []).length > 0;

  const botoes = [];
  if (temDestaques) {
    botoes.push(`<button type="button" class="category-tab" data-alvo="secao-destaques">Mais pedidos</button>`);
  }
  cats.forEach((cat, i) => {
    botoes.push(
      `<button type="button" class="category-tab" data-alvo="${idParaSeccao(i)}">${esc(cat)}</button>`
    );
  });

  bar.innerHTML = botoes.join('');

  bar.querySelectorAll('[data-alvo]').forEach((b) => {
    b.addEventListener('click', () => irParaSeccao(b.dataset.alvo));
  });

  marcarCategoriaActiva(temDestaques ? 'secao-destaques' : idParaSeccao(0));
}

// irParaSeccao rola até uma secção, compensando a altura da barra fixa.
function irParaSeccao(id) {
  const alvo = document.getElementById(id);
  if (!alvo) return;

  const barra = document.getElementById('categories-bar');
  const offset = (barra ? barra.offsetHeight : 0) + 12;
  const y = alvo.getBoundingClientRect().top + window.scrollY - offset;

  window.scrollTo({ top: y, behavior: 'smooth' });
  marcarCategoriaActiva(id);
}

// marcarCategoriaActiva destaca um separador e traz-no para dentro da barra.
//
// O scrollIntoView horizontal importa: com muitas categorias, a activa pode estar fora do
// que se vê, e o cliente perderia a referência de onde está no menu.
function marcarCategoriaActiva(id) {
  const bar = document.getElementById('categories-bar');
  if (!bar) return;

  bar.querySelectorAll('[data-alvo]').forEach((b) => {
    const activa = b.dataset.alvo === id;
    b.classList.toggle('active', activa);
    if (activa) {
      b.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'smooth' });
    }
  });
}

// renderProducts desenha todas as secções do menu.
function renderProducts() {
  const container = document.getElementById('menu-items-container');
  const itens = itensFiltrados();

  if (termoBusca && itens.length === 0) {
    container.innerHTML = `
      <div class="cart-empty glass">
        <div class="cart-empty-icon">🔎</div>
        <h3>Nada encontrado</h3>
        <p>Não há pratos com &laquo;${esc(termoBusca)}&raquo;. Tente outra palavra.</p>
      </div>`;
    return;
  }

  // Com pesquisa activa mostra-se uma lista única: agrupar por categoria dispersa
  // resultados que o cliente quer ver juntos.
  if (termoBusca) {
    container.innerHTML = seccaoHTML(
      'secao-busca',
      `${itens.length} ${itens.length === 1 ? 'resultado' : 'resultados'}`,
      itens
    );
    ligarBotoesDeItem(container);
    return;
  }

  const partes = [];

  const destaques = (menuData.destaques || [])
    .map((id) => menuData.itens.find((i) => i.id === id))
    .filter(Boolean);
  if (destaques.length > 0) {
    partes.push(seccaoHTML('secao-destaques', 'Mais pedidos', destaques, true));
  }

  categoriasDoMenu().forEach((cat, i) => {
    const daCategoria = menuData.itens.filter((it) => it.categoria === cat);
    partes.push(seccaoHTML(idParaSeccao(i), cat, daCategoria));
  });

  container.innerHTML = partes.join('');
  ligarBotoesDeItem(container);
  observarSeccoes();
}

// itensFiltrados aplica a pesquisa por nome e descrição.
function itensFiltrados() {
  if (!termoBusca) return menuData.itens;

  const t = normalizar(termoBusca);
  return menuData.itens.filter(
    (i) => normalizar(i.nome).includes(t) || normalizar(i.descricao || '').includes(t)
  );
}

// normalizar remove acentos e maiúsculas.
//
// Sem isto, procurar "frances" não encontraria "Francesinha" e "pao" não encontraria
// "Pão" — e é exactamente assim que as pessoas escrevem no telemóvel.
function normalizar(texto) {
  return String(texto)
    .toLowerCase()
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '');
}

function seccaoHTML(id, titulo, itens, destaque = false) {
  return `
    <section class="category-section" id="${id}">
      <h2 class="category-title${destaque ? ' category-title-destaque' : ''}">${esc(titulo)}</h2>
      <div class="items-grid">
        ${itens.map(cartaoItemHTML).join('')}
      </div>
    </section>`;
}

// cartaoItemHTML desenha um prato.
//
// Texto à esquerda e imagem à direita, como nas apps de comida: o nome e o preço são o que
// o cliente procura, e ficam alinhados na margem onde o olhar começa.
function cartaoItemHTML(item) {
  const temDesconto = item.desconto_ativo && item.preco_desconto_cents > 0;

  const precos = temDesconto
    ? `<span class="prod-original-price slashed">${esc(formatCents(item.preco_cents))}</span>
       <span class="prod-discount-price">${esc(formatCents(item.preco_desconto_cents))}</span>`
    : `<span class="prod-original-price">${esc(formatCents(item.preco_cents))}</span>`;

  const badge = temDesconto
    ? `<span class="discount-tag">-${Math.round((1 - item.preco_desconto_cents / item.preco_cents) * 100)}%</span>`
    : '';

  // Um prato sem fotografia não mostra uma imagem genérica: uma imagem de banco de
  // imagens que não é o prato engana o cliente. O cartão fica só com texto.
  const imagem = item.imagem_url
    ? `<div class="product-img" style="background-image: url('${escAttr(item.imagem_url)}')"></div>`
    : '';

  return `
    <article class="product-card" data-item="${esc(item.id)}" role="button" tabindex="0">
      <div class="product-details">
        <div class="product-info">
          <h3 class="product-name">${esc(item.nome)}</h3>
          <p class="product-desc">${esc(item.descricao || '')}</p>
        </div>
        <div class="product-footer">
          <div class="product-price-col">${precos}${badge}</div>
        </div>
      </div>
      ${imagem}
    </article>`;
}

// ligarBotoesDeItem torna todo o cartão tocável, e não apenas um botão "+".
function ligarBotoesDeItem(container) {
  container.querySelectorAll('[data-item]').forEach((card) => {
    const abrir = () => abrirDetalhe(Number(card.dataset.item));
    card.addEventListener('click', abrir);
    card.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        abrir();
      }
    });
  });
}

// observarSeccoes destaca na barra a categoria da secção visível.
//
// IntersectionObserver em vez de um listener de scroll: o listener corre a cada pixel e
// num telemóvel isso nota-se na fluidez do rolamento.
function observarSeccoes() {
  if (observadorSeccoes) observadorSeccoes.disconnect();
  if (!('IntersectionObserver' in window)) return;

  const barra = document.getElementById('categories-bar');
  const alturaBarra = barra ? barra.offsetHeight : 0;

  observadorSeccoes = new IntersectionObserver(
    (entradas) => {
      // A secção mais acima entre as visíveis é a que conta.
      const visiveis = entradas
        .filter((e) => e.isIntersecting)
        .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
      if (visiveis.length > 0) marcarCategoriaActiva(visiveis[0].target.id);
    },
    {
      // A margem superior desconta a barra fixa, para que a secção seja considerada
      // activa quando o seu título passa por baixo dela e não quando toca no topo do ecrã.
      rootMargin: `-${alturaBarra + 20}px 0px -70% 0px`,
      threshold: 0,
    }
  );

  document
    .querySelectorAll('.category-section')
    .forEach((sec) => observadorSeccoes.observe(sec));
}

// --- Pesquisa ---

function ligarBusca() {
  const campo = document.getElementById('menu-busca');
  const limpar = document.getElementById('menu-busca-limpar');
  if (!campo) return;

  let atrasado = null;
  campo.addEventListener('input', () => {
    // Espera curta: filtrar a cada tecla numa lista grande faz o teclado engasgar.
    clearTimeout(atrasado);
    atrasado = setTimeout(() => {
      termoBusca = campo.value.trim();
      if (limpar) limpar.hidden = termoBusca === '';
      renderProducts();
    }, 150);
  });

  if (limpar) {
    limpar.addEventListener('click', () => {
      campo.value = '';
      termoBusca = '';
      limpar.hidden = true;
      renderProducts();
      campo.focus();
    });
  }
}

// --- Detalhe do prato ---

// abrirDetalhe mostra o painel com a descrição completa, quantidade e observações.
function abrirDetalhe(id) {
  const item = menuData.itens.find((i) => i.id === id);
  if (!item) return;

  itemAberto = item;
  qtdAberta = 1;

  const imagem = document.getElementById('item-detalhe-imagem');
  if (item.imagem_url) {
    imagem.style.backgroundImage = `url('${escAttr(item.imagem_url)}')`;
    imagem.hidden = false;
  } else {
    imagem.style.backgroundImage = '';
    imagem.hidden = true;
  }

  document.getElementById('item-detalhe-nome').textContent = item.nome;

  const desc = document.getElementById('item-detalhe-descricao');
  desc.textContent = item.descricao || '';
  desc.hidden = !item.descricao;

  document.getElementById('item-detalhe-preco').textContent =
    formatCents(item.preco_efetivo_cents);

  const obs = document.getElementById('item-detalhe-obs');
  obs.value = '';
  document.getElementById('item-detalhe-obs-contador').textContent = '0';

  actualizarDetalhe();
  document.getElementById('item-detalhe-overlay').classList.add('open');
  // O foco vai para o painel para que o teclado e os leitores de ecrã sigam a abertura.
  document.getElementById('item-detalhe-fechar').focus();
}

function fecharDetalhe() {
  document.getElementById('item-detalhe-overlay').classList.remove('open');
  itemAberto = null;
}

// actualizarDetalhe mantém a quantidade e o total do botão em sincronia.
//
// O total no próprio botão é deliberado: o cliente vê quanto vai acrescentar antes de
// tocar, em vez de descobrir no carrinho.
function actualizarDetalhe() {
  if (!itemAberto) return;
  document.getElementById('item-detalhe-qtd').textContent = String(qtdAberta);
  document.getElementById('item-detalhe-adicionar').textContent =
    `Adicionar · ${formatCents(itemAberto.preco_efetivo_cents * qtdAberta)}`;
  document.getElementById('item-detalhe-menos').disabled = qtdAberta <= 1;
}

function ligarDetalhe() {
  const overlay = document.getElementById('item-detalhe-overlay');
  if (!overlay) return;

  document.getElementById('item-detalhe-fechar').addEventListener('click', fecharDetalhe);

  // Tocar fora do painel fecha, como é esperado numa folha inferior.
  overlay.addEventListener('click', (e) => {
    if (e.target === overlay) fecharDetalhe();
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && overlay.classList.contains('open')) fecharDetalhe();
  });

  document.getElementById('item-detalhe-menos').addEventListener('click', () => {
    if (qtdAberta > 1) qtdAberta--;
    actualizarDetalhe();
  });
  document.getElementById('item-detalhe-mais').addEventListener('click', () => {
    // Limite alinhado com o do servidor.
    if (qtdAberta < 99) qtdAberta++;
    actualizarDetalhe();
  });

  const obs = document.getElementById('item-detalhe-obs');
  obs.addEventListener('input', () => {
    document.getElementById('item-detalhe-obs-contador').textContent =
      String(obs.value.length);
  });

  document.getElementById('item-detalhe-adicionar').addEventListener('click', () => {
    if (!itemAberto) return;
    adicionarAoCarrinho(itemAberto, qtdAberta, obs.value.trim());
    fecharDetalhe();
  });
}

// Adiciona item ao carrinho
// adicionarAoCarrinho junta um prato ao carrinho.
//
// A chave da linha inclui as observações: o mesmo prato "sem cebola" e normal são duas
// linhas, tal como no servidor. Sem isso a instrução de um deles perdia-se.
function adicionarAoCarrinho(item, qtd, observacoes) {
  const chave = `${item.id}|${observacoes || ''}`;
  const existente = cart.find((c) => c.chave === chave);

  if (existente) {
    existente.qty = Math.min(99, existente.qty + qtd);
  } else {
    cart.push({
      chave,
      id: item.id,
      nome: item.nome,
      observacoes: observacoes || '',
      // O preço efectivo (com desconto, se activo) vem resolvido do servidor.
      precoCents: item.preco_efetivo_cents,
      taxaBP: item.taxa_iva_bp,
      qty: qtd,
    });
  }

  showToast(`${qtd}x ${item.nome} no carrinho`, 'success');
  updateCartUI();
}

// Altera quantidade no carrinho
function updateQty(chave, delta) {
  const index = cart.findIndex(c => c.chave === chave);
  if (index === -1) return;

  cart[index].qty += delta;
  if (cart[index].qty <= 0) {
    cart.splice(index, 1);
  }
  updateCartUI();
}

// Atualiza interfaces do carrinho (barra lateral e modal celular)
function updateCartUI() {
  const itemsContainer = document.getElementById('cart-items');
  const totalsContainer = document.getElementById('cart-totals');
  
  const totalItems = cart.reduce((sum, i) => sum + i.qty, 0);
  // Soma inteira em cêntimos: exacta, ao contrário da soma em euros com float.
  const totalCents = cart.reduce((sum, i) => sum + i.precoCents * i.qty, 0);

  // Badge do carrinho flutuante
  document.getElementById('cart-count-badge').innerText = totalItems;
  
  // Barra do carrinho celular
  const mobileBar = document.getElementById('mobile-cart-bar');
  if (totalItems > 0) {
    mobileBar.classList.add('active');
    document.getElementById('mobile-total').innerText = formatCents(totalCents);
    document.getElementById('mobile-qty').innerText = `${totalItems} ${totalItems === 1 ? 'item' : 'itens'}`;
  } else {
    mobileBar.classList.remove('active');
  }

  if (cart.length === 0) {
    itemsContainer.innerHTML = `
      <div class="cart-empty">
        <div class="cart-empty-icon">🛒</div>
        <p>Seu carrinho está vazio</p>
      </div>
    `;
    totalsContainer.style.display = 'none';
    document.getElementById('btn-checkout').disabled = true;
    return;
  }

  document.getElementById('btn-checkout').disabled = false;
  totalsContainer.style.display = 'flex';
  
  itemsContainer.innerHTML = cart.map(item => `
    <div class="cart-item">
      <div class="cart-item-details">
        <div class="cart-item-name">${esc(item.nome)}</div>
        ${item.observacoes ? `<div class="cart-item-obs">${esc(item.observacoes)}</div>` : ''}
        <div class="cart-item-price">${esc(formatCents(item.precoCents))}</div>
      </div>
      <div class="cart-item-qty">
        <button class="qty-btn" data-qty="${escAttr(item.chave)}" data-delta="-1">−</button>
        <span>${esc(item.qty)}</span>
        <button class="qty-btn" data-qty="${escAttr(item.chave)}" data-delta="1">+</button>
      </div>
    </div>
  `).join('');

  itemsContainer.querySelectorAll('[data-qty]').forEach(b => {
    b.addEventListener('click', () =>
      updateQty(b.dataset.qty, Number(b.dataset.delta))
    );
  });

  document.getElementById('subtotal-val').innerText = formatCents(totalCents);
  document.getElementById('total-val').innerText = formatCents(totalCents);

  // Nota de IVA e decomposição do carrinho.
  mostrarIVACarrinho();
}

// mostrarIVACarrinho apresenta a nota legal e o IVA contido no total.
//
// Em Portugal os preços afixados ao consumidor incluem imposto; o cliente paga o total
// mostrado. A decomposição aparece porque foi pedida explicitamente, e é calculada por
// taxa para que uma encomenda com pratos a 13% e bebidas a 23% feche ao cêntimo.
function mostrarIVACarrinho() {
  const alvo = document.getElementById('cart-iva');
  if (!alvo) return;

  if (cart.length === 0) {
    alvo.innerHTML = '';
    return;
  }

  // Agrupa por taxa, como o servidor faz, e extrai o IVA de cada grupo.
  const porTaxa = new Map();
  cart.forEach((i) => {
    const taxa = Number(i.taxaBP) || 0;
    porTaxa.set(taxa, (porTaxa.get(taxa) || 0) + i.precoCents * i.qty);
  });

  const linhas = [...porTaxa.entries()]
    .sort((a, b) => b[0] - a[0])
    .map(([taxa, bruto]) => {
      const iva = ivaIncluido(bruto, taxa);
      const etiqueta = taxa > 0 ? `${(taxa / 100).toString().replace('.', ',')}%` : 'isento';
      return `<div style="display:flex; justify-content:space-between;">
        <span>IVA ${esc(etiqueta)}</span><span>${esc(formatCents(iva))}</span>
      </div>`;
    });

  const ivaTotal = [...porTaxa.entries()].reduce(
    (soma, [taxa, bruto]) => soma + ivaIncluido(bruto, taxa), 0
  );

  alvo.innerHTML = `
    <div style="font-size:0.8rem; color:var(--text-muted); border-top:1px solid var(--border-color); padding-top:0.6rem; margin-top:0.6rem;">
      ${linhas.join('')}
      <div style="display:flex; justify-content:space-between; margin-top:0.25rem;">
        <span>Total de IVA incluído</span><span>${esc(formatCents(ivaTotal))}</span>
      </div>
      <p style="margin-top:0.5rem;">${esc(menuData?.restaurante?.nota_iva || 'Preços com IVA incluído')}</p>
    </div>
  `;
}

// Controla o fluxo de Checkout
function openCheckoutModal() {
  if (cart.length === 0) return;
  // Uma chave por tentativa de checkout, reutilizada em todos os retries dessa tentativa.
  chaveIdempotencia = uuid();
  document.getElementById('checkout-modal').classList.add('open');
  renderPaymentOptions();
  goToStep(1);
}

function closeCheckoutModal() {
  document.getElementById('checkout-modal').classList.remove('open');
}

function goToStep(step) {
  currentStep = step;
  
  // Atualiza indicadores visuais de progresso
  const stepItems = document.querySelectorAll('.step-item');
  stepItems.forEach((item, index) => {
    item.classList.remove('active', 'completed');
    if (index + 1 < step) item.classList.add('completed');
    else if (index + 1 === step) item.classList.add('active');
  });

  // Oculta todas as telas do checkout e exibe a correta
  document.getElementById('step-1-form').style.display = step === 1 ? 'block' : 'none';
  document.getElementById('step-2-payment').style.display = step === 2 ? 'block' : 'none';
  document.getElementById('step-3-sim').style.display = step === 3 ? 'block' : 'none';
  
  // Controla botões de navegação
  document.getElementById('checkout-back').style.display = step === 1 ? 'none' : 'inline-flex';
  document.getElementById('checkout-next').innerText = step === 3 ? 'Finalizar encomenda' : 'Continuar';
}

function handleNextStep() {
  if (currentStep === 1) {
    // Valida dados pessoais
    const nome = document.getElementById('checkout-nome').value.trim();
    const tel = document.getElementById('checkout-telefone').value.trim();
    if (!nome || !tel) {
      showToast('Indique o seu nome e telemóvel.', 'error');
      return;
    }
    goToStep(2);
  } else if (currentStep === 2) {
    // Valida método de pagamento selecionado
    const selected = document.querySelector('input[name="payment_method"]:checked');
    if (!selected) {
      showToast('Escolha um método de pagamento.', 'error');
      return;
    }
    setupConfirmationScreen(selected.value);
    goToStep(3);
  } else if (currentStep === 3) {
    // Processa o pedido e envia para o back-end
    submitOrderToServer();
  }
}

function handleBackStep() {
  if (currentStep > 1) {
    goToStep(currentStep - 1);
  }
}

// renderPaymentOptions mostra os métodos aceites na caixa.
//
// No MVP não existe pagamento na aplicação: ambas as opções são pagas no balcão ao
// levantar a encomenda.
function renderPaymentOptions() {
  const rest = menuData.restaurante;
  const container = document.getElementById('payment-options-container');

  const opcoes = [];

  if (rest.dinheiro_ativo) {
    opcoes.push(`
      <label class="payment-label">
        <input type="radio" name="payment_method" value="dinheiro">
        <div>
          <div style="font-weight: 600;">Dinheiro na caixa</div>
          <div class="payment-info-text">Paga em dinheiro quando levantar a encomenda</div>
        </div>
      </label>
    `);
  }

  if (rest.cartao_ativo) {
    opcoes.push(`
      <label class="payment-label">
        <input type="radio" name="payment_method" value="cartao">
        <div>
          <div style="font-weight: 600;">Cartão na caixa</div>
          <div class="payment-info-text">Paga com cartão no terminal do restaurante</div>
        </div>
      </label>
    `);
  }

  if (opcoes.length === 0) {
    container.innerHTML =
      '<p style="color:var(--danger)">Este restaurante ainda não configurou métodos de pagamento. Não é possível encomendar.</p>';
    document.getElementById('checkout-next').disabled = true;
    return;
  }

  document.getElementById('checkout-next').disabled = false;
  container.innerHTML = opcoes.join('');

  // Marca visualmente a opção escolhida sem depender de onclick inline.
  container.querySelectorAll('.payment-label').forEach((label) => {
    label.addEventListener('click', () => {
      container.querySelectorAll('.payment-label').forEach((l) => l.classList.remove('selected'));
      label.classList.add('selected');
      const radio = label.querySelector('input[type="radio"]');
      if (radio) radio.checked = true;
    });
  });
}

// setupConfirmationScreen monta o passo 3: confirmação do levantamento.
//
// Substitui o simulador de pagamento da versão anterior. Não há nada a processar aqui —
// o pagamento acontece fisicamente na caixa.
function setupConfirmationScreen(metodo) {
  const container = document.getElementById('step-3-sim');
  const totalCents = cart.reduce((soma, i) => soma + i.precoCents * i.qty, 0);

  const blocoTroco =
    metodo === 'dinheiro'
      ? `
      <div style="margin-top:1rem; padding-top:1rem; border-top:1px solid var(--border-color);">
        <label style="font-size:0.85rem; font-weight:600; display:block; margin-bottom:0.5rem;">Vai precisar de troco?</label>
        <label style="display:flex; align-items:center; gap:0.5rem; cursor:pointer;">
          <input type="checkbox" id="dinheiro-troco-checkbox" style="width:18px; height:18px; accent-color:var(--primary);">
          <span>Sim, indicar o valor com que vou pagar</span>
        </label>
        <div id="troco-input-wrapper" style="margin-top:0.75rem; display:none;">
          <input type="number" class="form-control" id="dinheiro-troco-val"
                 placeholder="Ex.: 20" step="0.01" min="0" inputmode="decimal">
          <p style="font-size:0.75rem; color:var(--text-muted); margin-top:0.35rem;">
            Ajuda a caixa a preparar o troco. Não é obrigatório.
          </p>
        </div>
      </div>`
      : '';

  const comoPaga =
    metodo === 'dinheiro'
      ? 'Paga em <strong>dinheiro</strong> na caixa.'
      : 'Paga com <strong>cartão</strong> no terminal da caixa.';

  container.innerHTML = `
    <div class="pix-container anim-fade">
      <h4 style="font-weight:700;">Confirmar encomenda</h4>
      <p style="font-size:0.9rem; color:var(--text-muted); margin:0.75rem 0;">
        Encomenda para <strong>levantamento ao balcão</strong>, no valor de
        <strong>${esc(formatCents(totalCents))}</strong>. ${comoPaga}
      </p>
      <p style="font-size:0.85rem; color:var(--text-muted);">
        Vai receber um link para acompanhar a preparação. Levante quando estiver pronta.
      </p>
      ${blocoTroco}
    </div>
  `;

  const cb = document.getElementById('dinheiro-troco-checkbox');
  if (cb) {
    cb.addEventListener('change', () => {
      document.getElementById('troco-input-wrapper').style.display = cb.checked ? 'block' : 'none';
    });
  }
}

// submitOrderToServer envia a encomenda.
async function submitOrderToServer() {
  const btn = document.getElementById('checkout-next');
  btn.disabled = true;
  btn.innerText = 'A enviar...';

  const restaurar = () => {
    btn.disabled = false;
    btn.innerText = 'Finalizar encomenda';
  };

  const escolhido = document.querySelector('input[name="payment_method"]:checked');
  if (!escolhido) {
    showToast('Escolha um método de pagamento.', 'error');
    restaurar();
    return;
  }

  const totalCents = cart.reduce((soma, i) => soma + i.precoCents * i.qty, 0);

  // O troco é enviado como texto, para que o servidor o converta em cêntimos sem passar
  // por vírgula flutuante.
  let trocoTexto = '';

  if (escolhido.value === 'dinheiro') {
    const cb = document.getElementById('dinheiro-troco-checkbox');
    if (cb && cb.checked) {
      trocoTexto = document.getElementById('dinheiro-troco-val').value.trim();
      const trocoCents = parseValor(trocoTexto);

      if (trocoTexto !== '' && trocoCents === null) {
        showToast('Indique um valor válido para o troco, por exemplo 20.', 'error');
        restaurar();
        return;
      }
      if (trocoCents !== null && trocoCents > 0 && trocoCents < totalCents) {
        showToast('O valor indicado é inferior ao total da encomenda.', 'error');
        restaurar();
        return;
      }
    }
  }

  const payload = {
    cliente_nome: document.getElementById('checkout-nome').value.trim(),
    cliente_telefone: document.getElementById('checkout-telefone').value.trim(),
    forma_pagamento: escolhido.value,
    troco_para_texto: trocoTexto,
    itens: cart.map((i) => ({
      menu_item_id: i.id,
      quantidade: i.qty,
      observacoes: i.observacoes || '',
    })),
  };

  try {
    const url = resolvedByDomain ? '/api/pedidos' : `/api/${tenantSlug}/pedidos`;
    const dados = await api(url, {
      metodo: 'POST',
      corpo: payload,
      autenticado: false,
      // A mesma chave em cada tentativa: o servidor devolve a encomenda já criada em vez
      // de criar uma segunda.
      idempotencyKey: chaveIdempotencia,
    });

    showToast('Encomenda registada.', 'success');

    cart = [];
    localStorage.removeItem(`cart_${tenantSlug}`);

    // O rastreio usa o token opaco: o número da encomenda não permite consultá-la.
    window.location.href = `/pedido?t=${encodeURIComponent(dados.encomenda.public_token)}`;
  } catch (err) {
    showToast(err.message, 'error');
    restaurar();
  }
}

// Configura eventos gerais do cardápio
function setupEventListeners() {
  ligarBusca();
  ligarDetalhe();

  document.getElementById('btn-checkout').addEventListener('click', openCheckoutModal);
  // A barra de carrinho em ecrã pequeno tinha um onclick inline, bloqueado pela CSP.
  const barraMovel = document.getElementById('mobile-cart-bar');
  if (barraMovel) barraMovel.addEventListener('click', openCheckoutModal);
  document.getElementById('checkout-close').addEventListener('click', closeCheckoutModal);
  document.getElementById('checkout-back').addEventListener('click', handleBackStep);
  document.getElementById('checkout-next').addEventListener('click', handleNextStep);
}
