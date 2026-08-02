// White label: aplica a identidade visual do restaurante ao storefront.
//
// O storefront tinha a marca da plataforma fixa no HTML (ícone "C", texto "MenuOnline"),
// pelo que o cliente final via a marca do SaaS em vez da do restaurante onde está a
// encomendar. Isto é o oposto do que se vende a um lojista: o argumento é "a sua loja
// online", não "a nossa plataforma com o seu menu dentro".
//
// Depende de common.js (esc, escAttr).

'use strict';

/**
 * aplicarBranding pinta a página com a identidade do restaurante.
 * @param {object} rest  o objecto "restaurante" devolvido por /api/public-menu
 */
function aplicarBranding(rest) {
  if (!rest) return;

  definirTitulo(rest);
  definirCores(rest);
  definirLogotipo(rest);
  definirDescricao(rest);
  definirFavicon(rest);
  definirAssinaturaPlataforma(rest);
}

// definirTitulo põe o nome do restaurante no separador do browser e nas meta tags.
//
// Importa para partilha em WhatsApp e para SEO local: o título anterior era
// "Cardápio Online - Faça Seu Pedido" em todos os restaurantes, o que os tornava
// indistinguíveis nos resultados de pesquisa.
function definirTitulo(rest) {
  const sufixo = document.body.dataset.tituloSufixo || '';
  document.title = sufixo ? `${rest.nome} — ${sufixo}` : rest.nome;

  definirMeta('og:title', rest.nome, 'property');
  definirMeta('og:site_name', rest.nome, 'property');
  if (rest.logo_url) definirMeta('og:image', rest.logo_url, 'property');
}

function definirDescricao(rest) {
  const desc = rest.descricao_curta
    ? rest.descricao_curta
    : `Veja o menu de ${rest.nome} e encomende para levantar ao balcão.`;

  definirMeta('description', desc, 'name');
  definirMeta('og:description', desc, 'property');

  // Subtítulo visível, quando a página tem espaço para ele.
  const el = document.getElementById('rest-tagline');
  if (el && rest.descricao_curta) {
    el.textContent = rest.descricao_curta;
    el.style.display = '';
  }
}

function definirMeta(chave, valor, atributo) {
  let el = document.querySelector(`meta[${atributo}="${chave}"]`);
  if (!el) {
    el = document.createElement('meta');
    el.setAttribute(atributo, chave);
    document.head.appendChild(el);
  }
  el.setAttribute('content', valor);
}

// definirCores substitui as variáveis CSS do tema pelas cores do restaurante.
//
// A cor do texto sobre a cor primária vem calculada do servidor a partir da luminância:
// uma cor de marca clara com texto branco por cima tornaria o botão de encomendar
// ilegível, e o lojista escolhe a cor sem pensar em contraste.
function definirCores(rest) {
  const raiz = document.documentElement;

  if (rest.cor_primaria) {
    raiz.style.setProperty('--primary', rest.cor_primaria);
    raiz.style.setProperty('--primary-dark', escurecer(rest.cor_primaria, 0.15));
    raiz.style.setProperty('--primary-light', clarear(rest.cor_primaria, 0.2));
    raiz.style.setProperty('--on-primary', rest.cor_texto_sobre_primaria || '#ffffff');

    // A cor da barra do browser em Android segue a marca.
    definirMetaThemeColor(rest.cor_primaria);
  }
  if (rest.cor_secundaria) {
    raiz.style.setProperty('--secondary', rest.cor_secundaria);
  }
}

function definirMetaThemeColor(cor) {
  let el = document.querySelector('meta[name="theme-color"]');
  if (!el) {
    el = document.createElement('meta');
    el.setAttribute('name', 'theme-color');
    document.head.appendChild(el);
  }
  el.setAttribute('content', cor);
}

// definirLogotipo troca o ícone da plataforma pelo logótipo do restaurante.
//
// Sem logótipo carregado usa as iniciais do nome, calculadas no servidor. Antes aparecia
// a letra "C" da plataforma, igual para todos os restaurantes.
function definirLogotipo(rest) {
  const icone = document.querySelector('.logo-icon');
  if (!icone) return;

  if (rest.logo_url) {
    icone.textContent = '';
    icone.style.backgroundImage = `url('${escAttr(rest.logo_url)}')`;
    icone.style.backgroundSize = 'cover';
    icone.style.backgroundPosition = 'center';
    icone.setAttribute('role', 'img');
    icone.setAttribute('aria-label', `Logótipo de ${rest.nome}`);
  } else {
    icone.textContent = rest.iniciais || '?';
  }

  const texto = document.querySelector('.logo-text');
  if (texto) texto.textContent = rest.nome;
}

function definirFavicon(rest) {
  if (!rest.logo_url) return;

  let link = document.querySelector('link[rel="icon"]');
  if (!link) {
    link = document.createElement('link');
    link.setAttribute('rel', 'icon');
    document.head.appendChild(link);
  }
  link.setAttribute('href', rest.logo_url);
}

// definirAssinaturaPlataforma mostra ou esconde a assinatura discreta no fundo.
//
// Desligá-la é uma funcionalidade de plano pago: white label completo é exactamente o
// tipo de coisa por que um restaurante paga mais.
function definirAssinaturaPlataforma(rest) {
  const el = document.getElementById('marca-plataforma');
  if (!el) return;

  if (rest.mostrar_marca_plataforma === false) {
    el.style.display = 'none';
    return;
  }
  el.style.display = '';
}

// --- Manipulação de cor ---

function componentes(hex) {
  const h = String(hex).replace('#', '');
  if (h.length !== 6) return null;
  return [
    parseInt(h.slice(0, 2), 16),
    parseInt(h.slice(2, 4), 16),
    parseInt(h.slice(4, 6), 16),
  ];
}

function paraHex([r, g, b]) {
  const clamp = (v) => Math.max(0, Math.min(255, Math.round(v)));
  return '#' + [r, g, b].map((v) => clamp(v).toString(16).padStart(2, '0')).join('');
}

function escurecer(hex, fraccao) {
  const c = componentes(hex);
  return c ? paraHex(c.map((v) => v * (1 - fraccao))) : hex;
}

function clarear(hex, fraccao) {
  const c = componentes(hex);
  return c ? paraHex(c.map((v) => v + (255 - v) * fraccao)) : hex;
}
