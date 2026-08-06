# Instruções para o Claude Code

Este projecto documenta-se em [`AGENTS.md`](AGENTS.md), no formato lido também pelo Codex,
Cursor, Gemini CLI e outros assistentes. **Lê esse ficheiro antes de alterar código** — em
particular a secção "Invariantes", que cobre dinheiro, autenticação e isolamento entre
restaurantes, e a secção "Armadilhas já encontradas".

Manter a documentação num só ficheiro é deliberado: duas fontes divergem, e a que estiver
errada é sempre a que alguém vai ler.

Resumo do que mais frequentemente se erra aqui:

- **Escreve em português (pt-PT).** Identificadores, comentários e texto de interface.
- **Dinheiro é `dinheiro.Cents`, nunca `float64`.** O IVA extrai-se do preço, não se soma.
- **`tenant_id` vem sempre das claims do JWT** nas rotas administrativas, nunca do `Host`.
- **Corre o ESLint** em `static/js/` — o `node --check` não apanha variáveis inexistentes.
- **Nada de `onclick=` nem `<script>` inline**: a CSP bloqueia-os silenciosamente.
- **Rota nova em `main.go`** tem de entrar também em `montarRotas`, no teste de isolamento.
