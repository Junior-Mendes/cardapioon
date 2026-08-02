// Ecrã de definição de nova senha, alcançado pelo link enviado por email.
//
// Depende de common.js (api, showToast).

'use strict';

const token = new URLSearchParams(window.location.search).get('token');

document.addEventListener('DOMContentLoaded', () => {
  if (!token) {
    document.getElementById('reset-form').style.display = 'none';
    document.getElementById('sem-token').style.display = 'block';
    return;
  }

  document.getElementById('reset-form').addEventListener('submit', submeter);
});

async function submeter(e) {
  e.preventDefault();

  const senha = document.getElementById('nova-senha').value;
  const confirmacao = document.getElementById('confirmar-senha').value;

  if (senha !== confirmacao) {
    showToast('As duas senhas não coincidem.', 'error');
    return;
  }
  // Espelha a regra do servidor, que valida o mesmo e é a autoridade.
  if (senha.length < 8 || !/[a-zA-Z]/.test(senha) || !/[0-9]/.test(senha)) {
    showToast('A senha precisa de pelo menos 8 caracteres, com letras e números.', 'error');
    return;
  }

  const btn = document.getElementById('btn-guardar');
  btn.disabled = true;
  btn.innerText = 'A guardar...';

  try {
    const dados = await api('/api/tenant/redefinir-senha', {
      metodo: 'POST',
      corpo: { token, password: senha },
      autenticado: false,
    });

    showToast(dados.message, 'success');

    // A redefinição revoga todas as sessões abertas; limpar a local evita ficar com um
    // token já inválido em localStorage.
    Sessao.limpar();
    setTimeout(() => {
      window.location.href = '/admin';
    }, 1500);
  } catch (err) {
    showToast(err.message, 'error');
    btn.disabled = false;
    btn.innerText = 'Guardar nova senha';
  }
}
