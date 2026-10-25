# App Lastro — acompanhamento financeiro

O app Android é a superfície de **consulta** das despesas e da saúde da coleta. Ele responde “o que está registrado?”, “onde se concentram minhas despesas?” e “posso confiar na atualização desta visão?”, e continua coletando as notificações do Google Wallet. Ele **nunca** cria, altera, exclui, confirma ou concilia uma despesa: registrar, corrigir, classificar e esclarecer continuam na conversa com a Case.

Este documento registra como o PRD do app (“Lastro App — Acompanhamento financeiro e dashboards”, versão 1.0 de 25/10/2026) foi implementado e as decisões tomadas na implementação. O detalhe técnico das rotas está em [`apps/api/README.md`](../apps/api/README.md#what-the-app-reads) e o das telas em [`apps/android/README.md`](../apps/android/README.md#screens).

## Destinos

| Destino | Pergunta | Fonte |
| --- | --- | --- |
| Visão geral | Como está o período? | `GET /v1/overview` |
| Despesas | O que compõe o valor? | `GET /v1/expenses` e `GET /v1/expenses/{id}` |
| Análises | O que mudou e onde? | `GET /v1/analysis` |
| Coleta | O que foi capturado? | capturas do celular e `GET /v1/wallet/notifications` |
| Configurações (no topo) | A coleta está pronta? | celular e `GET /v1/whoami` |

Todas as rotas de consulta exigem a permissão `expenses:read` e só leem. Os totais são calculados no servidor, com uma regra única, para que uma lista aberta por um gráfico some exatamente a barra de onde veio.

## Decisões

Decisões tomadas durante a implementação, em 25/10/2026, para os pontos que o PRD deixou em aberto (D01 a D06) e para o que surgiu no caminho.

### Base e métricas (D01, D02, D03)

- **Base principal:** data da compra, no fuso da conta. O recorte inicial é o **mês corrente até hoje**; meses fechados vão do dia 1 ao último dia. Mês futuro é recusado.
- **Moeda:** uma por vez, BRL por padrão; moedas nunca são somadas nem convertidas. A resposta lista as moedas presentes no período.
- **Total bruto (M01):** despesas **confirmadas** com **valor integral conhecido**. Ficam fora e aparecem à parte: despesas provisórias, despesas em conferência (dúvida aberta) e compras conhecidas só pela parcela (“sem valor integral conhecido”, contadas, nunca somadas). Parcela, cobrança de fatura e compromisso futuro nunca entram nessa base.
- **Estornos (M02):** pela **data do estorno**, só os confirmados, em valor positivo separado. **Líquido (M03)** = bruto − estornos, e pode ser negativo (o app explica que os estornos superaram as despesas).
- **Valor médio (M05):** bruto ÷ quantidade, ausente quando não há despesas (“não aplicável”, nunca zero).
- **Situação da categoria (G03):** categoria confirmada = **concluída**; sugerida pela Case = **classificação provisória**; sem categoria = **classificação pendente**. A situação da categoria é sempre mostrada separada da situação da despesa.

### Categorias (V02, M06)

- As **cinco maiores categorias concluídas** aparecem sozinhas; as demais concluídas vão para **“Outras”**, que só existe quando sobram duas ou mais (com uma só, ela aparece pelo nome).
- **Classificação provisória** e **Sem categoria** ficam sempre fora de “Outras”, em uma seção própria.
- Participação em pontos-base sobre o bruto; com bruto zero não há percentual. O app avisa que a soma dos percentuais arredondados pode não dar 100%.
- Cada grupo carrega o filtro da lista correspondente (período, moeda, situação, natureza, categorias e classificação).

### Cobertura e comparação (G04, V03, V04, M07)

- **Cobertura de cada dia:** completa, parcial ou desconhecida, conforme o acompanhamento automático (pausas e intervalos de mais de uma hora sem sinal do celular); dias ainda por vir aparecem como futuros. Dias sem cobertura ficam hachurados e nunca viram “zero”.
- **Comparação:** com os **mesmos dias** do mês anterior (1 até hoje no mês em andamento; mês inteiro contra mês inteiro nos fechados).
- A comparação fica **indisponível** quando o acompanhamento ainda não cobria o período anterior (“sem histórico”) ou quando **24 horas ou mais** ficaram sem cobertura em qualquer um dos dois períodos (“lacuna”).
- Notificações ainda não lidas pela Case **não suprimem** a comparação: aparecem como aviso junto ao total. Com base anterior zero, o app mostra a diferença e “sem base para variação percentual”.

### Análises (A01 a A04, D05)

- Janela de **seis meses** (de 1 a 12 na API), terminando no mês corrente, que aparece marcado como “em andamento” e nunca disputa posição com meses fechados.
- Cada mês tem cobertura completa, parcial (24 horas ou mais sem cobertura) ou nenhuma (“sem dados no Lastro”).
- A variação por categoria compara só **categorias concluídas** nos mesmos dias e só aparece quando a comparação está disponível; provisórias e sem categoria são apenas contadas, com aviso de que a leitura pode mudar.
- A **curva acumulada** ficou **fora** (primeiro corte de escopo do PRD); o valor médio entrou.

### Coleta (C01 a C04, D04)

- Recorte: notificações **capturadas nos últimos 7 dias**, pela data de captura no celular.
- Situações atuais exclusivas, que somam o total capturado: aguardando envio, aguardando resultado, processada, com impedimento e situação indisponível. **Enviadas** é um marco cumulativo, mostrado à parte e nunca somado.
- “Só no celular” (envio desligado), “não guardada: acompanhamento pausado”, “descartada” e “recusada” contam como **com impedimento**, com o motivo.
- O resultado das processadas é mostrado sem criar despesa artificial: originou despesa, era da mesma despesa de outra notificação, não representava gasto ou virou dúvida. Uma notificação que virou despesa abre a despesa (a lista do servidor passou a trazer o `transaction_id`).

### Navegação, privacidade e conexão (D05, D06, S01 a S03, P01, P02)

- Quatro destinos (Visão geral, Despesas, Análises, Coleta) e Configurações no topo.
- **Ocultar valores:** desligado por padrão, persistente, alternado pelo olho no topo. Oculta valores em resumos, gráficos, listas e detalhes e também o texto das notificações. Contagens, categorias, situações e percentuais continuam visíveis.
- **Proteger a prévia do app:** ligado por padrão. Esconde o conteúdo na tela de apps recentes sem bloquear capturas de tela feitas pela própria pessoa.
- **Credencial:** a mesma do celular, agora com `wallet:ingest` **e** `expenses:read`. Configurações mostra o que a credencial permite e avisa quando falta a leitura. Ela fica mascarada depois de salva e nunca aparece em telas ou mensagens.
- **Pausar a coleta** afeta só novas capturas: o histórico fica, e o intervalo pausado não é recuperado depois.
- **Sem conexão:** a última Visão geral do mês corrente fica salva só no celular (fora de backup) e aparece com a data em que foi salva. Ela é apagada quando a credencial é trocada ou removida.

### Lista e detalhe (E01 a E06)

- Mais recentes primeiro (data, hora conhecida, ordem de registro), 50 por página, com o total de cada dia.
- Filtros: período, categoria (os mesmos grupos da Visão geral, incluindo “Outras”, provisória e sem categoria), classificação, situação e busca por descrição ou estabelecimento, sem diferenciar maiúsculas e acentos. O recorte e a quantidade de resultados ficam visíveis, e “Limpar filtros” volta ao mês.
- O detalhe explica por que um registro está fora do total e de onde ele veio (notificações e fatura). Quando não há vínculo, mostra “associação não disponível”, sem botão para criá-lo.

## O que fica para depois

- Curva acumulada comparativa (A04), se a comparação mensal mostrar que ela acrescenta compreensão.
- Visão por cobrança de fatura no app, com totais próprios, nunca somados à visão por compra.
- Recorte de captura configurável na Coleta (hoje fixo em 7 dias).
