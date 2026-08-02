// Lógica do Cardápio Online & Checkout do Cliente

let menuData = null;
let cart = [];
let activeCategory = 'all';
let currentStep = 1;

// Utilitário para formatar moeda brasileira
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

// Obtém o slug do restaurante da URL (ex: ?tenant=bella-italia)
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

  if (!tenantSlug) {
    tenantSlug = 'bella-italia';
  }

  loadRestaurantMenu();
}

// Busca o cardápio público no back-end
async function loadRestaurantMenu() {
  try {
    const url = resolvedByDomain ? '/api/public-menu' : `/api/${tenantSlug}/public-menu`;
    const res = await fetch(url);
    if (!res.ok) throw new Error('Não foi possível carregar o cardápio deste restaurante.');
    
    menuData = await res.json();
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

// Renderiza o cabeçalho do restaurante
function renderRestaurantHeader() {
  const rest = menuData.restaurante;
  document.getElementById('rest-name').innerText = rest.nome;
  document.getElementById('rest-title').innerText = rest.nome;
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
    const label = cat === 'all' ? 'Todos os Pratos' : cat;
    const activeClass = cat === activeCategory ? 'active' : '';
    return `<div class="category-tab ${activeClass}" onclick="filterCategory('${cat}')">${label}</div>`;
  }).join('');
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
    const hasDiscount = item.desconto_ativo && item.preco_desconto > 0;
    const finalPrice = hasDiscount ? item.preco_desconto : item.preco;
    
    const priceDisplay = hasDiscount 
      ? `<span class="prod-original-price slashed">${formatCurrency(item.preco)}</span>
         <span class="prod-discount-price">${formatCurrency(item.preco_desconto)}</span>`
      : `<span class="prod-original-price">${formatCurrency(item.preco)}</span>`;

    const discountBadge = hasDiscount 
      ? `<div class="discount-tag">-${Math.round((1 - item.preco_desconto / item.preco) * 100)}%</div>`
      : '';

    const imgUrl = item.imagem_url || 'https://images.unsplash.com/photo-1546069901-ba9599a7e63c?w=500';

    return `
      <div class="product-card glass">
        <div class="product-img" style="background-image: url('${imgUrl}')">
          ${discountBadge}
        </div>
        <div class="product-details">
          <div class="product-info">
            <h3 class="product-name">${item.nome}</h3>
            <p class="product-desc">${item.descricao || ''}</p>
          </div>
          <div class="product-footer">
            <div class="product-price-col">
              ${priceDisplay}
            </div>
            <button class="add-btn" onclick="addToCart(${item.id})">+</button>
          </div>
        </div>
      </div>
    `;
  }).join('');
}

// Adiciona item ao carrinho
function addToCart(id) {
  const item = menuData.itens.find(i => i.id === id);
  if (!item) return;

  const existing = cart.find(c => c.id === id);
  if (existing) {
    existing.qty++;
  } else {
    const hasDiscount = item.desconto_ativo && item.preco_desconto > 0;
    const price = hasDiscount ? item.preco_desconto : item.preco;
    cart.push({
      id: item.id,
      nome: item.nome,
      preco: price,
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
  const totalValue = cart.reduce((sum, i) => sum + (i.preco * i.qty), 0);

  // Badge do carrinho flutuante
  document.getElementById('cart-count-badge').innerText = totalItems;
  
  // Barra do carrinho celular
  const mobileBar = document.getElementById('mobile-cart-bar');
  if (totalItems > 0) {
    mobileBar.classList.add('active');
    document.getElementById('mobile-total').innerText = formatCurrency(totalValue);
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
        <div class="cart-item-name">${item.nome}</div>
        <div class="cart-item-price">${formatCurrency(item.preco)}</div>
      </div>
      <div class="cart-item-qty">
        <button class="qty-btn" onclick="updateQty(${item.id}, -1)">-</button>
        <span>${item.qty}</span>
        <button class="qty-btn" onclick="updateQty(${item.id}, 1)">+</button>
      </div>
    </div>
  `).join('');

  document.getElementById('subtotal-val').innerText = formatCurrency(totalValue);
  document.getElementById('total-val').innerText = formatCurrency(totalValue);
}

// Controla o fluxo de Checkout
function openCheckoutModal() {
  if (cart.length === 0) return;
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
  document.getElementById('checkout-next').innerText = step === 3 ? 'Finalizar Pedido' : 'Continuar';
}

function handleNextStep() {
  if (currentStep === 1) {
    // Valida dados pessoais
    const nome = document.getElementById('checkout-nome').value.trim();
    const tel = document.getElementById('checkout-telefone').value.trim();
    if (!nome || !tel) {
      showToast('Por favor, preencha seu nome e telefone.', 'error');
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
    setupSimulationScreen(selected.value);
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

// Renderiza as opções de pagamento ativas do lojista
function renderPaymentOptions() {
  const rest = menuData.restaurante;
  const container = document.getElementById('payment-options-container');
  
  let options = [];
  
  if (rest.pix_ativo) {
    options.push(`
      <label class="payment-label" onclick="selectRadio(this)">
        <input type="radio" name="payment_method" value="pix">
        <div>
          <div style="font-weight: 600;">PIX (Online)</div>
          <div class="payment-info-text">Pague na hora e seu pedido é confirmado instantaneamente</div>
        </div>
      </label>
    `);
  }
  
  if (rest.cartao_credito_ativo) {
    options.push(`
      <label class="payment-label" onclick="selectRadio(this)">
        <input type="radio" name="payment_method" value="cartao_credito">
        <div>
          <div style="font-weight: 600;">Cartão de Crédito (Online)</div>
          <div class="payment-info-text">Pague online com cartão de crédito via plataforma simulada</div>
        </div>
      </label>
    `);
  }

  if (rest.dinheiro_ativo) {
    options.push(`
      <label class="payment-label" onclick="selectRadio(this)">
        <input type="radio" name="payment_method" value="retirada_dinheiro">
        <div>
          <div style="font-weight: 600;">Dinheiro (na Retirada)</div>
          <div class="payment-info-text">Pague em dinheiro no balcão ao retirar seu pedido</div>
        </div>
      </label>
    `);
  }

  if (rest.cartao_debito_ativo) {
    options.push(`
      <label class="payment-label" onclick="selectRadio(this)">
        <input type="radio" name="payment_method" value="retirada_cartao">
        <div>
          <div style="font-weight: 600;">Cartão de Débito/Crédito (na Retirada)</div>
          <div class="payment-info-text">Pague na maquininha física do restaurante na retirada</div>
        </div>
      </label>
    `);
  }

  if (options.length === 0) {
    container.innerHTML = `<p style="color:var(--danger)">Erro: O restaurante não configurou nenhum método de pagamento ativo!</p>`;
    document.getElementById('checkout-next').disabled = true;
  } else {
    container.innerHTML = options.join('');
    document.getElementById('checkout-next').disabled = false;
  }
}

function selectRadio(label) {
  document.querySelectorAll('.payment-label').forEach(l => l.classList.remove('selected'));
  label.classList.add('selected');
  const input = label.querySelector('input[type="radio"]');
  if (input) input.checked = true;
}

// Configura a tela de simulação de pagamento (Passo 3)
function setupSimulationScreen(method) {
  const container = document.getElementById('simulation-content');
  const totalVal = cart.reduce((sum, i) => sum + (i.preco * i.qty), 0);

  if (method === 'pix') {
    container.innerHTML = `
      <div class="pix-container anim-fade">
        <h4 style="font-weight:700;">Simulador de Pagamento PIX</h4>
        <p style="font-size:0.9rem; text-align:center; color:var(--text-muted);">
          Escaneie o QR Code abaixo ou copie a chave Pix Copia e Cola para pagar o valor de <strong>${formatCurrency(totalVal)}</strong>.
        </p>
        <div class="pix-qr">
          <img src="https://api.qrserver.com/v1/create-qr-code/?size=150x150&data=00020101021226850014br.gov.bcb.pix2563${encodeURIComponent(menuData.restaurante.pix_chave)}5405${totalVal.toFixed(2)}5802BR" style="width:100%;" alt="QR Code PIX">
        </div>
        <div class="pix-key-box">
          <input type="text" readonly class="form-control" id="pix-copy-key" value="${menuData.restaurante.pix_chave}">
          <button class="btn btn-primary" onclick="copyPixKey()">Copiar</button>
        </div>
        <p style="font-size:0.8rem; color:var(--success); font-weight:600;">✓ PIX Simulador ativado. Chave do lojista: ${menuData.restaurante.pix_chave}</p>
      </div>
    `;
  } else if (method === 'cartao_credito') {
    container.innerHTML = `
      <div class="anim-fade" style="display:flex; flex-direction:column; align-items:center;">
        <h4 style="font-weight:700;">Pagamento com Cartão de Crédito</h4>
        
        <!-- Cartão 3D Interativo -->
        <div class="flip-card" id="credit-card-preview">
          <div class="flip-card-inner">
            <div class="card-front">
              <div class="card-chip"></div>
              <div class="card-number" id="card-preview-number">•••• •••• •••• ••••</div>
              <div class="card-row">
                <div>
                  <div class="card-label">Titular</div>
                  <div class="card-holder" id="card-preview-name">NOME COMPLETO</div>
                </div>
                <div>
                  <div class="card-label">Validade</div>
                  <div class="card-expiry" id="card-preview-expiry">MM/AA</div>
                </div>
              </div>
            </div>
            <div class="card-back">
              <div class="card-stripe"></div>
              <div class="card-signature" id="card-preview-cvv">•••</div>
            </div>
          </div>
        </div>

        <div style="width:100%; max-width:320px; display:flex; flex-direction:column; gap:0.75rem;">
          <input type="text" class="form-control" id="cc-number" placeholder="Número do Cartão (16 dígitos)" maxlength="19" oninput="formatCC(this)" onfocus="flipCard(false)">
          <input type="text" class="form-control" id="cc-name" placeholder="Nome Impresso no Cartão" oninput="updateCCName(this)" onfocus="flipCard(false)">
          <div style="display:flex; gap:0.5rem;">
            <input type="text" class="form-control" id="cc-expiry" placeholder="MM/AA" maxlength="5" oninput="formatExpiry(this)" onfocus="flipCard(false)">
            <input type="text" class="form-control" id="cc-cvv" placeholder="CVV" maxlength="4" oninput="updateCVV(this)" onfocus="flipCard(true)" onblur="flipCard(false)">
          </div>
        </div>
      </div>
    `;
  } else if (method === 'retirada_dinheiro') {
    container.innerHTML = `
      <div class="pix-container anim-fade">
        <h4 style="font-weight:700;">Pagamento em Dinheiro na Retirada</h4>
        <p style="font-size:0.9rem; text-align:center; color:var(--text-muted);">
          Você efetuará o pagamento de <strong>${formatCurrency(totalVal)}</strong> no balcão do restaurante.
        </p>
        <div style="width:100%; max-width:300px; text-align:left;">
          <label style="font-size:0.85rem; font-weight:600; display:block; margin-bottom:0.5rem;">Precisa de troco?</label>
          <div style="display:flex; gap:0.5rem; align-items:center;">
            <input type="checkbox" id="dinheiro-troco-checkbox" onchange="toggleTrocoInput(this)" style="width:18px; height:18px; accent-color:var(--primary);">
            <span>Sim, preciso de troco</span>
          </div>
          <div id="troco-input-wrapper" style="margin-top:0.75rem; display:none;">
            <input type="number" class="form-control" id="dinheiro-troco-val" placeholder="Troco para quanto? (ex: 50 ou 100)" step="0.01">
          </div>
        </div>
      </div>
    `;
  } else if (method === 'retirada_cartao') {
    container.innerHTML = `
      <div class="pix-container anim-fade">
        <h4 style="font-weight:700;">Pagamento com Cartão na Retirada</h4>
        <p style="font-size:0.9rem; text-align:center; color:var(--text-muted);">
          O motoboy ou atendente levará a maquininha física até você no momento da retirada.
        </p>
        <p style="font-size:0.85rem; color:var(--success); text-align:center;">
          ✓ Aceitamos as principais bandeiras de débito, crédito e refeição (Visa, Master, Elo, Sodexo).
        </p>
      </div>
    `;
  }
}

// Auxiliares de Cartão 3D
function flipCard(back) {
  const card = document.getElementById('credit-card-preview');
  if (back) card.classList.add('flipped');
  else card.classList.remove('flipped');
}

function formatCC(input) {
  let val = input.value.replace(/\D/g, '');
  if (val.length > 16) val = val.slice(0,16);
  
  let formatted = '';
  for (let i = 0; i < val.length; i++) {
    if (i > 0 && i % 4 === 0) formatted += ' ';
    formatted += val[i];
  }
  input.value = formatted;
  document.getElementById('card-preview-number').innerText = formatted || '•••• •••• •••• ••••';
}

function updateCCName(input) {
  document.getElementById('card-preview-name').innerText = input.value.toUpperCase() || 'NOME COMPLETO';
}

function formatExpiry(input) {
  let val = input.value.replace(/\D/g, '');
  if (val.length > 4) val = val.slice(0, 4);
  if (val.length > 2) {
    input.value = val.slice(0,2) + '/' + val.slice(2);
  } else {
    input.value = val;
  }
  document.getElementById('card-preview-expiry').innerText = input.value || 'MM/AA';
}

function updateCVV(input) {
  let val = input.value.replace(/\D/g, '');
  if (val.length > 4) val = val.slice(0,4);
  input.value = val;
  document.getElementById('card-preview-cvv').innerText = val || '•••';
}

// Auxiliar de Troco
function toggleTrocoInput(cb) {
  document.getElementById('troco-input-wrapper').style.display = cb.checked ? 'block' : 'none';
}

// Copiar chave PIX
function copyPixKey() {
  const key = document.getElementById('pix-copy-key');
  key.select();
  navigator.clipboard.writeText(key.value);
  showToast('Chave PIX copiada para a área de transferência!', 'success');
}

// Submete o pedido ao servidor
async function submitOrderToServer() {
  const btn = document.getElementById('checkout-next');
  btn.disabled = true;
  btn.innerText = 'Enviando...';

  const nome = document.getElementById('checkout-nome').value.trim();
  const tel = document.getElementById('checkout-telefone').value.trim();
  const paymentMethod = document.querySelector('input[name="payment_method"]:checked').value;
  
  let trocoPara = 0.0;
  let lastDigits = '';

  if (paymentMethod === 'retirada_dinheiro') {
    const needTroco = document.getElementById('dinheiro-troco-checkbox').checked;
    if (needTroco) {
      trocoPara = parseFloat(document.getElementById('dinheiro-troco-val').value) || 0.0;
      const totalVal = cart.reduce((sum, i) => sum + (i.preco * i.qty), 0);
      if (trocoPara <= totalVal) {
        showToast('O valor para troco deve ser maior que o valor total do pedido!', 'error');
        btn.disabled = false;
        btn.innerText = 'Finalizar Pedido';
        return;
      }
    }
  } else if (paymentMethod === 'cartao_credito') {
    const ccNum = document.getElementById('cc-number').value.replace(/\s/g, '');
    const ccName = document.getElementById('cc-name').value.trim();
    const ccExp = document.getElementById('cc-expiry').value;
    const ccCvv = document.getElementById('cc-cvv').value;

    if (ccNum.length < 16 || !ccName || ccExp.length < 5 || ccCvv.length < 3) {
      showToast('Preencha os dados do cartão de crédito corretamente.', 'error');
      btn.disabled = false;
      btn.innerText = 'Finalizar Pedido';
      return;
    }
    // Simula loading de processamento de cartão
    await new Promise(resolve => setTimeout(resolve, 1500));
    lastDigits = ccNum.slice(-4);
  }

  // Prepara itens para o formato da API
  const apiItens = cart.map(i => ({
    menu_item_id: i.id,
    quantidade: i.qty
  }));

  const payload = {
    cliente_nome: nome,
    cliente_telefone: tel,
    forma_pagamento: paymentMethod,
    troco_para: trocoPara,
    cartao_ultimos_digitos: lastDigits,
    itens: apiItens
  };

  try {
    const url = resolvedByDomain ? '/api/pedidos' : `/api/${tenantSlug}/pedidos`;
    const response = await fetch(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(payload)
    });

    const data = await response.json();
    if (!response.ok) throw new Error(data.error || 'Erro ao registrar pedido.');

    showToast('Pedido realizado com sucesso!', 'success');
    
    // Limpa carrinho
    cart = [];
    localStorage.removeItem(`cart_${tenantSlug}`);
    
    // Redireciona o cliente para a tela de rastreamento do pedido
    setTimeout(() => {
      window.location.href = `/pedido?id=${data.order.id}`;
    }, 1000);

  } catch (err) {
    showToast(err.message, 'error');
    btn.disabled = false;
    btn.innerText = 'Finalizar Pedido';
  }
}

// Configura eventos gerais do cardápio
function setupEventListeners() {
  document.getElementById('btn-checkout').addEventListener('click', openCheckoutModal);
  document.getElementById('checkout-close').addEventListener('click', closeCheckoutModal);
  document.getElementById('checkout-back').addEventListener('click', handleBackStep);
  document.getElementById('checkout-next').addEventListener('click', handleNextStep);
}
