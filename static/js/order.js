// Lógica do Rastreamento do Pedido do Cliente

let pollingInterval = null;
const urlParams = new URLSearchParams(window.location.search);
const orderId = urlParams.get('id');

function formatCurrency(val) {
  return new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' }).format(val);
}

document.addEventListener('DOMContentLoaded', () => {
  if (!orderId) {
    document.getElementById('order-tracking-card').innerHTML = `
      <div class="cart-empty">
        <div class="cart-empty-icon">❌</div>
        <h3>Pedido não informado</h3>
        <p>Por favor, use um link de pedido válido.</p>
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
    const res = await fetch(`/api/pedidos/${orderId}`);
    if (!res.ok) throw new Error('Pedido não localizado.');

    const order = await res.json();
    renderOrderDetails(order);
    updateTrackerStatus(order.status);
    
    // Para o polling se o pedido for finalizado ou cancelado
    if (order.status === 'finalizado' || order.status === 'cancelado') {
      clearInterval(pollingInterval);
    }
  } catch (err) {
    clearInterval(pollingInterval);
    document.getElementById('order-tracking-card').innerHTML = `
      <div class="cart-empty">
        <div class="cart-empty-icon">⚠️</div>
        <h3>Erro ao rastrear pedido</h3>
        <p>${err.message}</p>
        <button class="btn btn-secondary" onclick="window.location.reload()" style="margin-top: 1rem;">Tentar Novamente</button>
      </div>
    `;
  }
}

function renderOrderDetails(order) {
  document.getElementById('track-rest-name').innerText = order.restaurante_nome;
  document.getElementById('track-order-id').innerText = `#${order.id}`;
  
  const date = new Date(order.created_at);
  document.getElementById('track-time').innerText = date.toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit' }) + ' - ' + date.toLocaleDateString('pt-BR');
  
  document.getElementById('track-client-name').innerText = order.cliente_nome;
  document.getElementById('track-client-phone').innerText = order.cliente_telefone;
  
  // Detalhes do Pagamento
  let paymentText = '';
  switch (order.forma_pagamento) {
    case 'pix':
      paymentText = 'PIX (Pago Online)';
      break;
    case 'cartao_credito':
      paymentText = `Cartão de Crédito Online (Final ****${order.cartao_ultimos_digitos || '0000'})`;
      break;
    case 'retirada_dinheiro':
      paymentText = 'Dinheiro na Retirada';
      if (order.troco_para > 0) {
        paymentText += ` (Levar troco para ${formatCurrency(order.troco_para)})`;
      }
      break;
    case 'retirada_cartao':
      paymentText = 'Cartão na Retirada';
      break;
  }
  document.getElementById('track-payment').innerText = paymentText;

  // Renderiza itens
  const container = document.getElementById('track-items-list');
  container.innerHTML = order.itens.map(item => `
    <div style="display:flex; justify-content:space-between; margin-bottom:0.5rem; font-size:0.95rem;">
      <div>
        <strong>${item.quantidade}x</strong> ${item.nome}
      </div>
      <div>
        ${formatCurrency(item.preco_unitario * item.quantidade)}
      </div>
    </div>
  `).join('');

  document.getElementById('track-total').innerText = formatCurrency(order.valor_total);
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
    document.getElementById('track-status-badge').innerText = 'PEDIDO CANCELADO';
    document.getElementById('track-status-desc').innerHTML = `
      <div style="font-weight:700; font-size:1.1rem; color:var(--danger);">Seu pedido foi cancelado</div>
      <p style="color:var(--text-muted); font-size:0.9rem; margin-top:0.25rem;">Entre em contato com o restaurante para mais informações.</p>
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
    badge.innerText = 'AGUARDANDO RESTAURANTE';
    desc.innerHTML = `
      <div style="font-weight:700; font-size:1.15rem; color:#fff;">Recebemos seu pedido!</div>
      <p style="color:var(--text-muted); font-size:0.9rem; margin-top:0.25rem;">Aguardando a confirmação do restaurante para iniciar a preparação.</p>
    `;
  } else if (status === 'preparando') {
    badge.innerText = 'EM PREPARAÇÃO';
    desc.innerHTML = `
      <div style="font-weight:700; font-size:1.15rem; color:var(--warning);">Seu pedido está sendo preparado!</div>
      <p style="color:var(--text-muted); font-size:0.9rem; margin-top:0.25rem;">Nossa equipe de cozinha está preparando seu prato com todo capricho.</p>
    `;
  } else if (status === 'pronto') {
    badge.innerText = 'DISPONÍVEL PARA RETIRADA';
    badge.style.backgroundColor = 'var(--success)';
    desc.innerHTML = `
      <div style="font-weight:800; font-size:1.25rem; color:var(--success);" class="anim-pulse">✓ Pedido pronto para retirada!</div>
      <p style="color:#fff; font-size:0.95rem; margin-top:0.5rem; font-weight:500;">
        Você já pode se dirigir ao balcão do restaurante para retirar seu pedido. Informe o código <strong>#${orderId}</strong>.
      </p>
    `;
  } else if (status === 'finalizado') {
    badge.innerText = 'RETIRADO / FINALIZADO';
    badge.style.backgroundColor = 'var(--info)';
    desc.innerHTML = `
      <div style="font-weight:700; font-size:1.15rem; color:var(--info);">Pedido finalizado!</div>
      <p style="color:var(--text-muted); font-size:0.9rem; margin-top:0.25rem;">Obrigado pela preferência e bom apetite!</p>
    `;
  }
}
