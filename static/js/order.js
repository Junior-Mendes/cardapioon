// Acompanhamento da encomenda pelo cliente.
//
// Depende de common.js (esc, formatCurrency, formatDateTime, showToast).
//
// O identificador na URL passou a ser o token opaco (?t=...). A versão anterior usava o
// ID sequencial (?id=...), o que permitia a qualquer pessoa iterar de 1 a N e ler o nome,
// o telefone e a forma de pagamento das encomendas de todos os restaurantes.

'use strict';

let pollingInterval = null;
let numeroEncomenda = null;
const urlParams = new URLSearchParams(window.location.search);
const orderToken = urlParams.get('t');

document.addEventListener('DOMContentLoaded', () => {
  if (!orderToken) {
    document.getElementById('order-tracking-card').innerHTML = `
      <div class="cart-empty">
        <div class="cart-empty-icon">❌</div>
        <h3>Link incompleto</h3>
        <p>Use o link de acompanhamento que recebeu ao fazer a encomenda.</p>
      </div>
    `;
    return;
  }

  loadOrderDetails();
  // Inicia Polling a cada 5 segundos
  pollingInterval = setInterval(loadOrderDetails, 5000);
});

async function loadOrderDetails() {
  try {
    const res = await fetch(`/api/encomendas/${encodeURIComponent(orderToken)}`);
    if (!res.ok) throw new Error('Encomenda não encontrada.');

    const order = await res.json();
    numeroEncomenda = order.numero;
    renderOrderDetails(order);
    updateTrackerStatus(order.status);
    
    // Para o polling se o pedido for finalizado ou cancelado
    if (order.status === 'finalizado' || order.status === 'cancelado') {
      clearInterval(pollingInterval);
    }
  } catch (err) {
    clearInterval(pollingInterval);
    const card = document.getElementById('order-tracking-card');
    card.innerHTML = `
      <div class="cart-empty">
        <div class="cart-empty-icon">⚠️</div>
        <h3>Não foi possível acompanhar a encomenda</h3>
        <p>${esc(err.message)}</p>
        <button class="btn btn-secondary" id="btn-tentar-novamente" style="margin-top: 1rem;">Tentar novamente</button>
      </div>
    `;
    // addEventListener em vez de onclick: a CSP não permite handlers inline.
    card.querySelector('#btn-tentar-novamente')
      .addEventListener('click', () => window.location.reload());
  }
}

function renderOrderDetails(order) {
  // A página de acompanhamento também é do restaurante, não da plataforma.
  aplicarBranding({
    nome: order.restaurante_nome,
    logo_url: order.restaurante_logo_url,
    cor_primaria: order.restaurante_cor_primaria,
    cor_texto_sobre_primaria: order.restaurante_cor_texto,
    iniciais: order.restaurante_iniciais,
    mostrar_marca_plataforma: order.mostrar_marca_plataforma,
  });

  document.getElementById('track-rest-name').innerText = order.restaurante_nome;
  document.getElementById('track-order-id').innerText = `#${order.numero}`;

  document.getElementById('track-time').innerText = formatDateTime(order.created_at);
  
  document.getElementById('track-client-name').innerText = order.cliente_nome;
  document.getElementById('track-client-phone').innerText = order.cliente_telefone;
  
  // Pagamento. No MVP é sempre na caixa, ao levantar; os valores antigos são mantidos
  // para que encomendas anteriores continuem legíveis.
  const ETIQUETAS = {
    dinheiro: 'Dinheiro na caixa',
    cartao: 'Cartão na caixa',
    retirada_dinheiro: 'Dinheiro na caixa',
    retirada_cartao: 'Cartão na caixa',
    cartao_credito: 'Cartão (histórico)',
    pix: 'Pix (histórico)',
  };

  let paymentText = ETIQUETAS[order.forma_pagamento] || order.forma_pagamento;
  if (Number(order.troco_para_cents) > 0) {
    paymentText += ` — vai pagar com ${formatCents(order.troco_para_cents)}`;
  }
  document.getElementById('track-payment').innerText = paymentText;

  // Renderiza itens
  const container = document.getElementById('track-items-list');
  container.innerHTML = order.itens.map(item => `
    <div style="display:flex; justify-content:space-between; margin-bottom:0.5rem; font-size:0.95rem;">
      <div>
        <strong>${esc(item.quantidade)}x</strong> ${esc(item.nome)}
      </div>
      <div>
        ${esc(formatCents(item.total_linha_cents))}
      </div>
    </div>
  `).join('');

  document.getElementById('track-total').innerText = formatCents(order.valor_total_cents);

  // Decomposição de IVA: o cliente vê exactamente quanto do total é imposto, e o balcão
  // tem o detalhe por taxa para reconciliar com o software de facturação.
  mostrarDecomposicaoIVA(order);

  // O botão de voltar precisa do slug, que só chega com a encomenda.
  const btnVoltar = document.getElementById('btn-back-menu');
  if (btnVoltar && !btnVoltar.dataset.ligado) {
    btnVoltar.dataset.ligado = '1';
    btnVoltar.addEventListener('click', () => {
      window.location.href = `/menu?tenant=${encodeURIComponent(order.restaurante_slug)}`;
    });
  }
}

// mostrarDecomposicaoIVA apresenta o detalhe por taxa, tal como aparece num talão.
function mostrarDecomposicaoIVA(order) {
  const alvo = document.getElementById('track-iva');
  if (!alvo) return;

  const linhas = order.linhas_iva || [];
  if (linhas.length === 0) {
    alvo.innerHTML = '';
    return;
  }

  const filas = linhas.map((l) => `
    <div style="display:flex; justify-content:space-between; font-size:0.85rem;">
      <span>Base a ${esc(l.taxa_iva_texto)}</span><span>${esc(l.base_texto)}</span>
    </div>
    <div style="display:flex; justify-content:space-between; font-size:0.85rem;">
      <span>IVA ${esc(l.taxa_iva_texto)}</span><span>${esc(l.iva_texto)}</span>
    </div>`).join('');

  alvo.innerHTML = `
    <div style="border-top:1px solid var(--border-color); padding-top:0.75rem; margin-top:0.75rem;">
      ${filas}
      <div style="display:flex; justify-content:space-between; font-weight:600; margin-top:0.4rem;">
        <span>Total de IVA</span><span>${esc(formatCents(order.iva_cents))}</span>
      </div>
      <p style="font-size:0.78rem; color:var(--text-muted); margin-top:0.6rem;">
        ${esc(order.nota_iva || 'Valores com IVA incluído')}
      </p>
    </div>
  `;
}

// Atualiza o gráfico do progresso do pedido e o texto explicativo
function updateTrackerStatus(status) {
  const steps = ['pendente', 'preparando', 'pronto', 'finalizado'];
  const currentIdx = steps.indexOf(status);

  // Tratando Cancelado
  if (status === 'cancelado') {
    document.getElementById('tracker-bar-fill').style.width = '100%';
    document.getElementById('tracker-bar-fill').style.backgroundColor = 'var(--danger)';
    document.getElementById('track-status-badge').className = 'restaurant-badge';
    document.getElementById('track-status-badge').style.backgroundColor = 'var(--danger)';
    document.getElementById('track-status-badge').innerText = 'ENCOMENDA CANCELADA';
    document.getElementById('track-status-desc').innerHTML = `
      <div style="font-weight:700; font-size:1.1rem; color:var(--danger);">A sua encomenda foi cancelada</div>
      <p style="color:var(--text-muted); font-size:0.9rem; margin-top:0.25rem;">Contacte o restaurante para mais informações.</p>
    `;
    
    // Desativa bolinhas de progresso
    document.querySelectorAll('.tracker-dot').forEach(dot => {
      dot.style.borderColor = 'var(--border-color)';
      dot.style.backgroundColor = 'var(--bg-primary)';
    });
    return;
  }

  // Preenche a barra de progresso
  let widthPercent = 0;
  if (currentIdx >= 0) {
    widthPercent = (currentIdx / (steps.length - 1)) * 100;
  }
  document.getElementById('tracker-bar-fill').style.width = `${widthPercent}%`;
  document.getElementById('tracker-bar-fill').style.backgroundColor = 'var(--success)';

  // Atualiza as bolinhas de progresso
  const dots = document.querySelectorAll('.tracker-dot');
  dots.forEach((dot, idx) => {
    dot.classList.remove('active', 'completed');
    if (idx < currentIdx) {
      dot.classList.add('completed');
    } else if (idx === currentIdx) {
      dot.classList.add('active');
    }
  });

  // Atualiza os textos do status ativo
  const badge = document.getElementById('track-status-badge');
  const desc = document.getElementById('track-status-desc');
  
  badge.className = 'restaurant-badge';
  badge.style.backgroundColor = 'var(--primary)';

  if (status === 'pendente') {
    badge.innerText = 'À ESPERA DE CONFIRMAÇÃO';
    desc.innerHTML = `
      <div style="font-weight:700; font-size:1.15rem; color:#fff;">Recebemos a sua encomenda!</div>
      <p style="color:var(--text-muted); font-size:0.9rem; margin-top:0.25rem;">O restaurante vai confirmar e começar a preparar.</p>
    `;
  } else if (status === 'preparando') {
    badge.innerText = 'EM PREPARAÇÃO';
    desc.innerHTML = `
      <div style="font-weight:700; font-size:1.15rem; color:var(--warning);">A sua encomenda está a ser preparada!</div>
      <p style="color:var(--text-muted); font-size:0.9rem; margin-top:0.25rem;">A cozinha já está a trabalhar no seu pedido.</p>
    `;
  } else if (status === 'pronto') {
    badge.innerText = 'PRONTA PARA LEVANTAR';
    badge.style.backgroundColor = 'var(--success)';
    desc.innerHTML = `
      <div style="font-weight:800; font-size:1.25rem; color:var(--success);" class="anim-pulse">✓ A sua encomenda está pronta!</div>
      <p style="color:#fff; font-size:0.95rem; margin-top:0.5rem; font-weight:500;">
        Pode dirigir-se ao balcão para levantar. Indique o número <strong>#${esc(numeroEncomenda)}</strong>.
      </p>
    `;
  } else if (status === 'finalizado') {
    badge.innerText = 'LEVANTADA';
    badge.style.backgroundColor = 'var(--info)';
    desc.innerHTML = `
      <div style="font-weight:700; font-size:1.15rem; color:var(--info);">Encomenda concluída!</div>
      <p style="color:var(--text-muted); font-size:0.9rem; margin-top:0.25rem;">Obrigado e bom apetite!</p>
    `;
  }
}
