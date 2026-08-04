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
let activeCategory = 'all';
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

// Renderiza a barra de categorias
function renderCategories() {
  const categories = ['all'];
  menuData.itens.forEach(item => {
    if (!categories.includes(item.categoria)) {
      categories.push(item.categoria);
    }
  });

  const bar = document.getElementById('categories-bar');
  bar.innerHTML = categories.map(cat => {
    const label = cat === 'all' ? 'Todos os pratos' : cat;
    const activeClass = cat === activeCategory ? 'active' : '';
    // data-categoria em vez de onclick: a CSP não permite handlers inline, e o nome da
    // categoria vem do lojista, pelo que interpolá-lo em JavaScript seria injecção.
    return `<div class="category-tab ${activeClass}" data-categoria="${esc(cat)}">${esc(label)}</div>`;
  }).join('');

  bar.querySelectorAll('[data-categoria]').forEach(tab => {
    tab.addEventListener('click', () => filterCategory(tab.dataset.categoria));
  });
}

// Filtra produtos pela categoria
function filterCategory(cat) {
  activeCategory = cat;
  renderCategories();
  renderProducts();
}

// Renderiza os produtos na tela
function renderProducts() {
  const container = document.getElementById('menu-items-container');
  const filtered = activeCategory === 'all' 
    ? menuData.itens 
    : menuData.itens.filter(i => i.categoria === activeCategory);

  if (filtered.length === 0) {
    container.innerHTML = `<div class="cart-empty glass" style="grid-column: 1/-1;"><p>Nenhum item disponível nesta categoria.</p></div>`;
    return;
  }

  container.innerHTML = filtered.map(item => {
    const hasDiscount = item.desconto_ativo && item.preco_desconto_cents > 0;

    // Todos os valores vêm em cêntimos do servidor; nada é recalculado aqui.
    const priceDisplay = hasDiscount
      ? `<span class="prod-original-price slashed">${esc(formatCents(item.preco_cents))}</span>
         <span class="prod-discount-price">${esc(formatCents(item.preco_desconto_cents))}</span>`
      : `<span class="prod-original-price">${esc(formatCents(item.preco_cents))}</span>`;

    const discountBadge = hasDiscount
      ? `<div class="discount-tag">-${Math.round((1 - item.preco_desconto_cents / item.preco_cents) * 100)}%</div>`
      : '';

    // escAttr remove parênteses: o URL entra num url(...) de CSS e sem isso poderia
    // fechá-lo e injectar mais estilos.
    const imgUrl = escAttr(item.imagem_url || 'https://images.unsplash.com/photo-1546069901-ba9599a7e63c?w=500');

    return `
      <div class="product-card glass">
        <div class="product-img" style="background-image: url('${imgUrl}')">
          ${discountBadge}
        </div>
        <div class="product-details">
          <div class="product-info">
            <h3 class="product-name">${esc(item.nome)}</h3>
            <p class="product-desc">${esc(item.descricao || '')}</p>
          </div>
          <div class="product-footer">
            <div class="product-price-col">
              ${priceDisplay}
            </div>
            <button class="add-btn" data-adicionar="${esc(item.id)}">+</button>
          </div>
        </div>
      </div>
    `;
  }).join('');

  // Handlers por addEventListener: a CSP não permite onclick inline.
  container.querySelectorAll('[data-adicionar]').forEach(b => {
    b.addEventListener('click', () => addToCart(Number(b.dataset.adicionar)));
  });
}

// Adiciona item ao carrinho
function addToCart(id) {
  const item = menuData.itens.find(i => i.id === id);
  if (!item) return;

  const existing = cart.find(c => c.id === id);
  if (existing) {
    existing.qty++;
  } else {
    // O preço efectivo (com desconto, se activo) vem resolvido do servidor.
    cart.push({
      id: item.id,
      nome: item.nome,
      precoCents: item.preco_efetivo_cents,
      taxaBP: item.taxa_iva_bp,
      qty: 1
    });
  }
  
  showToast(`"${item.nome}" adicionado ao carrinho!`, 'success');
  updateCartUI();
}

// Altera quantidade no carrinho
function updateQty(id, delta) {
  const index = cart.findIndex(c => c.id === id);
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
        <div class="cart-item-price">${esc(formatCents(item.precoCents))}</div>
      </div>
      <div class="cart-item-qty">
        <button class="qty-btn" data-qty="${esc(item.id)}" data-delta="-1">-</button>
        <span>${esc(item.qty)}</span>
        <button class="qty-btn" data-qty="${esc(item.id)}" data-delta="1">+</button>
      </div>
    </div>
  `).join('');

  itemsContainer.querySelectorAll('[data-qty]').forEach(b => {
    b.addEventListener('click', () =>
      updateQty(Number(b.dataset.qty), Number(b.dataset.delta))
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
    itens: cart.map((i) => ({ menu_item_id: i.id, quantidade: i.qty })),
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
  document.getElementById('btn-checkout').addEventListener('click', openCheckoutModal);
  // A barra de carrinho em ecrã pequeno tinha um onclick inline, bloqueado pela CSP.
  const barraMovel = document.getElementById('mobile-cart-bar');
  if (barraMovel) barraMovel.addEventListener('click', openCheckoutModal);
  document.getElementById('checkout-close').addEventListener('click', closeCheckoutModal);
  document.getElementById('checkout-back').addEventListener('click', handleBackStep);
  document.getElementById('checkout-next').addEventListener('click', handleNextStep);
}
