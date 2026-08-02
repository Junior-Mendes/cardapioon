// Registo de novos restaurantes na landing page.
//
// Depende de common.js (api, Sessao, showToast).
//
// Este código vivia num <script> inline dentro de index.html. Com a CSP em vigor
// (script-src 'self', sem 'unsafe-inline') os blocos inline não executam, pelo que o
// formulário de registo deixava de fazer absolutamente nada — sem erro visível ao
// utilizador, apenas uma violação de CSP na consola.

'use strict';

document.addEventListener('DOMContentLoaded', () => {
  const form = document.getElementById('signup-form');
  if (form) form.addEventListener('submit', registar);

  // Mostra o domínio real da plataforma junto ao campo do endereço, em vez de um
  // valor fixo no HTML.
  const sufixo = document.getElementById('slug-suffix');
  if (sufixo) sufixo.innerText = '.' + window.location.host.replace(/^www\./, '');
});

async function registar(e) {
  e.preventDefault();

  const btn = e.target.querySelector('button[type="submit"]');
  const textoOriginal = btn.innerText;
  btn.disabled = true;
  btn.innerText = 'A criar conta...';

  const payload = {
    nome: document.getElementById('reg-nome').value.trim(),
    slug: document.getElementById('reg-slug').value.trim(),
    email: document.getElementById('reg-email').value.trim(),
    password: document.getElementById('reg-password').value,
  };

  // Espelha as regras do servidor para dar resposta imediata; o servidor valida o mesmo
  // e é a autoridade.
  if (payload.password.length < 8) {
    showToast('A senha tem de ter pelo menos 8 caracteres, com letras e números.', 'error');
    btn.disabled = false;
    btn.innerText = textoOriginal;
    return;
  }

  try {
    const dados = await api('/api/tenant/registrar', {
      metodo: 'POST',
      corpo: payload,
      autenticado: false,
    });

    // O registo já devolve uma sessão válida: o lojista entra directamente no painel.
    Sessao.guardar(dados);

    showToast('Conta criada. A abrir o seu painel...', 'success');
    setTimeout(() => {
      // O parâmetro faz o painel abrir directamente onde o endereço está a ser
      // preparado, em vez de deixar o lojista à procura.
      window.location.href = '/admin?novo=1';
    }, 1200);
  } catch (err) {
    showToast(err.message, 'error');
    btn.disabled = false;
    btn.innerText = textoOriginal;
  }
}
