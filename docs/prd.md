# Requisitos do controle pessoal de despesas

Versão 2.1 — 22 de outubro de 2026

## 1. Objetivo

Permitir que Rudolfo acompanhe suas despesas ao longo do dia e confira os valores cobrados nas faturas, reunindo três formas de registro:

- Despesas informadas a Case por texto ou voz.
- Notificações de compras do Google Wallet no Android.
- Faturas mensais enviadas em PDF.

O controle deve mostrar quanto foi gasto, em quê, quando e por qual meio de pagamento, sem contar a mesma compra mais de uma vez. Também deve distinguir informações provisórias, cobranças confirmadas e valores ainda pendentes de esclarecimento.

Este documento define o que o controle deve oferecer e como seus resultados serão avaliados. As escolhas de implementação ficam fora do escopo.

## 2. Princípios

1. Uma compra pode aparecer em várias fontes, mas não deve virar várias despesas.
2. Informações desconhecidas devem permanecer identificadas como desconhecidas. Não inventar valores, datas, categorias ou finalidade de uma compra.
3. Notificação de compra e cobrança em fatura representam evidências diferentes. Uma não substitui automaticamente a outra.
4. O usuário deve conseguir entender a origem e a composição dos totais apresentados.
5. Correções devem preservar o histórico do que mudou e por quê.
6. Ausência de notificações não significa ausência de gastos.
7. Os dados devem permanecer privados e sob controle de Rudolfo. O controle pode atender outras contas, mas os dados de cada uma ficam isolados e só são acessíveis a quem ela autorizar.

## 3. Formas de registrar despesas

### 3.1. Pela conversa com Case

- Aceitar informações por texto e voz, como “Gastei R$ 327,39 no mercado agora”.
- Registrar valor, descrição, data e demais informações fornecidas.
- Permitir registro parcial quando faltar uma informação que não impeça identificar a despesa, como o meio de pagamento.
- Pedir esclarecimento quando a dúvida puder mudar materialmente o valor ou a identidade da compra.
- Confirmar ao usuário o que foi registrado e indicar eventuais informações pendentes.
- Interpretar “agora” com base no momento da mensagem, deixando essa interpretação identificada.
- Não criar uma nova despesa apenas porque a mesma mensagem ou transcrição foi recebida novamente.
- Permitir que o usuário informe posteriormente categoria, estabelecimento, meio de pagamento ou outra correção.

### 3.2. Pelas notificações do Google Wallet

- Incorporar automaticamente as notificações elegíveis de compras recebidas no Android, depois que esse acompanhamento for ativado pelo usuário.
- Mostrar as compras observadas o mais próximo possível do recebimento da notificação.
- Distinguir compra, atualização, cancelamento, estorno e aviso sem relação com uma despesa.
- Não transformar avisos de segurança ou mensagens de conteúdo incerto em despesas confirmadas.
- Não duplicar uma compra quando sua notificação for repetida ou atualizada.
- Não interpretar a remoção de uma notificação como cancelamento da compra.
- Mostrar quando houver atraso, interrupção ou cobertura incompleta no acompanhamento.
- Reconhecer que algumas compras podem não aparecer nas notificações e precisarão entrar pela fatura ou pela conversa.

### 3.3. Pelo PDF da fatura

- Aceitar o envio mensal da fatura em PDF.
- Identificar emissão, fechamento, vencimento e período a partir do conteúdo disponível, não do nome do arquivo ou da data de envio.
- Identificar compras, parcelas, Pix parcelados, juros, tarifas, impostos, créditos, estornos, pagamentos e saldo anterior.
- Permitir conferir cada informação com sua localização no documento original.
- Mostrar dúvidas de leitura ou trechos que não puderam ser interpretados com segurança.
- Não duplicar lançamentos quando a mesma fatura for enviada novamente.
- Comparar versões diferentes da mesma fatura e sinalizar alterações que exijam revisão.
- Informar quando a leitura estiver incompleta. Uma fatura parcialmente interpretada não pode ser apresentada como totalmente conferida.

## 4. Informações necessárias

### 4.1. Sobre cada compra ou movimentação

- Descrição original e descrição compreensível para consulta.
- Estabelecimento ou beneficiário, quando conhecido.
- Valor e moeda.
- Data da compra e horário, quando disponíveis.
- Meio de pagamento e identificação simples do cartão, quando informados.
- Natureza da movimentação: despesa, encargo, crédito, estorno, pagamento, transferência ou ajuste.
- Categoria e finalidade, incluindo se foram sugeridas ou confirmadas pelo usuário.
- Origem das informações: conversa, notificação, fatura ou outra fonte autorizada.
- Situação: provisória, confirmada ou pendente de esclarecimento.
- Relação com uma fatura, parcelamento, estorno ou correção, quando aplicável.

Datas incompletas e informações inferidas devem continuar identificadas como tais. Valores corrigidos não devem apagar o que constava originalmente na fonte.

### 4.2. Sobre cada fatura

- Cartão e emissor identificados de forma simples, sem expor dados desnecessários.
- Datas e período conhecidos.
- Total informado no documento.
- Composição do total: saldo anterior, compras, parcelas, encargos, pagamentos e créditos.
- Total calculado a partir dos itens e eventual diferença em relação à fatura.
- Situação da conferência e pendências existentes.

Uma fatura conferida não deve ser apresentada como paga sem evidência do pagamento.

### 4.3. Sobre parcelamentos

- Compra ou contratação a que pertencem.
- Parcela atual e quantidade total de parcelas, quando conhecidas.
- Valor cobrado no período.
- Valor total contratado, quando disponível.
- Principal, juros e outros encargos, quando identificáveis.
- Parcelas futuras conhecidas ou estimadas, com essa distinção explícita.
- Data de referência das informações sobre compromissos futuros.

## 5. Visões dos gastos

O controle deve oferecer visões separadas, sem somar valores que representam momentos diferentes da mesma compra.

### 5.1. Compras realizadas no período

Responder quanto foi comprado em determinado dia, semana ou mês, usando a data da compra. Separar valores confirmados e provisórios.

Em compras parceladas, mostrar o valor total da compra somente quando conhecido. Se apenas uma parcela estiver disponível, não inventar o valor integral.

### 5.2. Valores cobrados na fatura

Mostrar o que está sendo cobrado em cada ciclo, incluindo as parcelas daquele período e os encargos pertinentes. Não acrescentar novamente o valor integral das compras parceladas.

### 5.3. Compromissos futuros

Mostrar parcelas ainda previstas, indicando o que é conhecido e o que é estimado. Informar a data de referência para não apresentar uma projeção antiga como situação atual.

### 5.4. Pagamentos e saldo anterior

Usar pagamentos da fatura, amortizações e saldo transportado para explicar a cobrança e sua quitação. Esses valores não são novas despesas das compras já registradas.

Toda consulta deve deixar clara a visão utilizada. Se a pergunta permitir interpretações diferentes, apresentar a premissa adotada ou separar as respostas.

## 6. Conferência entre as fontes

- Relacionar informações da conversa, notificações e fatura quando corresponderem à mesma compra.
- Preservar compras legítimas de mesma loja, valor e data. Semelhança não é prova de duplicidade.
- Apresentar possíveis correspondências para revisão quando houver dúvida.
- Explicar por que uma correspondência foi sugerida e quais diferenças existem.
- Considerar diferenças entre a data da compra e a data em que ela aparece na fatura.
- Permitir associar uma compra às suas parcelas e reconhecer componentes de uma cobrança.
- Conferir notificações recebidas com atraso contra compras já encontradas nas faturas.
- Registrar normalmente compras que apareçam apenas no PDF.
- Manter pendente uma notificação sem correspondente, sem presumir que foi cancelada ou cobrada indevidamente.
- Permitir desfazer uma associação incorreta sem perder as informações originais.

Enquanto existir possível sobreposição não resolvida, mostrar os valores confirmados e os pendentes separadamente. Não apresentar a soma de ambos como total definitivo.

## 7. Categorias e finalidade

- Separar finalidade da compra e forma de pagamento. “Pix parcelado”, por exemplo, não explica em que o dinheiro foi gasto.
- Permitir categorias como Mercado, Alimentação e refeições, Conveniência — bebidas e snacks, Combustível, Transporte, Casa e reforma, Saúde, Educação, Vestuário, Eletrônicos, Software e assinaturas, Lazer, Encargos financeiros e Não identificado.
- Manter refrigerantes, chocolates e energéticos comprados em conveniência separados de refeições, mercado e combustível.
- Não classificar todo lançamento de posto da mesma forma sem informação suficiente.
- Usar a identificação do estabelecimento como auxílio, sem presumir o produto comprado em lojas com várias atividades ou marketplaces.
- Destacar classificações sugeridas e classificações confirmadas.
- Reunir dúvidas de finalidade em uma lista clara, com descrição, data e valor, para Rudolfo responder.
- Preservar correções feitas pelo usuário em novas conferências da mesma compra.
- Aplicar uma orientação a compras futuras somente quando o usuário indicar que ela deve valer como regra.

## 8. Pix parcelado e encargos

- Incluir todos os Pix parcelados cobrados na fatura, mesmo quando a finalidade ainda não for conhecida.
- Mostrar tanto a finalidade quanto o total gasto por essa modalidade.
- Separar principal, juros, IOF e tarifas quando o documento permitir.
- Não acrescentar novamente juros que já estejam incluídos no valor da parcela.
- Se a composição não estiver disponível, manter o valor total e sinalizar a falta do detalhamento.
- Contabilizar tarifas cobradas separadamente uma única vez.
- Distinguir pagamentos por bens e serviços de transferências ou refinanciamentos, mantendo dúvidas visíveis.

Exemplo: uma parcela de R$ 500,00 composta por R$ 450,00 de principal e R$ 50,00 de juros representa R$ 500,00 no total, não R$ 550,00. O subtotal por modalidade também não deve ser somado novamente às categorias.

## 9. Estornos, moedas e precisão

- Preservar a compra original e seu estorno, inclusive parcial ou ocorrido em outro ciclo.
- Permitir consultar valores brutos, créditos e resultado líquido.
- Não tratar compra recusada como despesa confirmada.
- Se não for possível saber se uma compra foi efetivada, mantê-la provisória.
- Preservar valor e moeda originais de compras internacionais e o valor cobrado em reais, quando informado.
- Não somar moedas diferentes nem inventar uma conversão.
- Fazer os valores fecharem no centavo. Diferenças devem ser explicadas, não escondidas em ajustes.
- Usar o horário local de Rudolfo nas consultas por dia e semana, distinguindo data da compra e data de recebimento da informação.

## 10. Consultas e resumos

O usuário deve poder perguntar, por exemplo:

- Quanto gastei no mercado até agora?
- Quais foram os gastos da semana?
- Quanto está sendo cobrado nesta fatura?
- Quanto estou gastando com Pix parcelado e quanto disso são juros?
- Quanto ainda tenho de parcelas futuras?
- Quais despesas estão sem categoria?
- O que falta conferir nesta fatura?

Cada resumo deve informar período, visão adotada, moeda, totais confirmados, valores provisórios e pendências relevantes. Deve ser possível consultar os lançamentos que compõem cada total.

Também precisamos de:

- Filtros por categoria, estabelecimento, modalidade, cartão, fatura e situação.
- Resumo diário das despesas do dia anterior, enviado pela manhã após definição do horário e ativação.
- Identificação de dados atrasados ou incompletos.
- Possibilidade de apresentar os resultados em mensagem e, posteriormente, em uma página privada ou imagem de dashboard.
- Exportação dos registros em formato que o usuário possa consultar e reutilizar.

O horário do resumo diário ainda precisa ser definido. Não considerar que esse envio já está ativo.

## 11. Correções, privacidade e continuidade

- Permitir corrigir descrição, categoria, data, valor e relações entre registros, mantendo o motivo e o histórico das alterações.
- Mostrar claramente quando uma solicitação foi concluída, ficou pendente ou falhou. Não confirmar um registro que não foi efetivado.
- Evitar que uma tentativa repetida após uma falha crie outra despesa.
- Preservar o que já foi registrado durante interrupções e deixar claro o que não foi recuperado.
- Manter os dados acessíveis somente a pessoas autorizadas pelo usuário.
- Manter os dados de cada conta isolados dos das demais. Uma conta só é criada por convite, e o titular pode revogar a qualquer momento os acessos que concedeu.
- Não incorporar senhas, códigos de autenticação, números completos de cartão ou notificações sem relação com as despesas.
- Permitir que o usuário interrompa o acompanhamento automático e consulte os dados já registrados.
- Ter possibilidade de recuperação dos registros e de exportação independente.
- Não publicar despesas pessoais em páginas abertas.

O acompanhamento deve ser rápido quando as informações estiverem disponíveis, mas não pode prometer registrar uma compra que não gerou notificação. Atrasos e lacunas precisam ser perceptíveis ao usuário.

## 12. Critérios de aceite

1. **Registro por conversa:** informar R$ 327,39 no mercado cria uma despesa nessa categoria. Receber novamente a mesma mensagem não a duplica; uma segunda compra real de mesmo valor continua permitida.
2. **Repetição de notificação:** receber ou atualizar várias vezes a mesma notificação não gera várias despesas.
3. **Mesma compra em três fontes:** conversa, notificação e PDF podem ser relacionados à mesma compra, sem triplicar seu valor.
4. **Compras iguais legítimas:** duas compras de R$ 32,97 na mesma loja e data permanecem distintas quando houver evidência de duas operações. Uma correspondência ambígua fica para revisão.
5. **Compra parcelada:** R$ 1.200,00 em 12 parcelas de R$ 100,00 aparece como R$ 1.200,00 na visão da compra e R$ 100,00 na fatura do ciclo. Após a primeira parcela, o compromisso restante conhecido é R$ 1.100,00. Esses valores não são somados como uma única despesa.
6. **Pix com juros:** R$ 500,00 contendo R$ 50,00 de juros resulta em principal de R$ 450,00 e encargos de R$ 50,00, sem dupla contagem.
7. **Finalidade desconhecida:** um Pix sem finalidade continua visível no valor cobrado e na lista de dúvidas. Classificá-lo depois não muda o total da fatura.
8. **Conveniência:** bebidas e snacks ficam separados de refeições, mercado e combustível.
9. **Fatura repetida:** reenviar o mesmo PDF não cria novos lançamentos. Uma versão alterada destaca as diferenças para conferência.
10. **Estorno parcial:** uma compra de R$ 100,00 com estorno de R$ 30,00 preserva ambos e permite visualizar líquido de R$ 70,00 no período pertinente.
11. **Cobertura incompleta:** quando faltarem informações, o resumo mostra a limitação em vez de afirmar que não houve gastos.
12. **Fatura conferida:** o total calculado e a composição explicam o total informado. Diferenças ou leitura incompleta impedem apresentá-la como totalmente conferida.
13. **Correção preservada:** uma categoria confirmada pelo usuário não é sobrescrita silenciosamente ao conferir novamente a compra.
14. **Privacidade:** consultas e resumos não expõem informações a pessoas não autorizadas nem incluem dados secretos desnecessários.

## 13. Prioridades e decisões pendentes

### Primeira entrega

- Registro por conversa, notificações do Google Wallet e PDF.
- Categorias e dúvidas de finalidade.
- Conferência entre fontes sem duplicidade.
- Separação entre compras, cobranças mensais e compromissos futuros.
- Tratamento de Pix parcelado, encargos e estornos.
- Consultas por período e categoria, com pendências e limitações visíveis.
- Correções, privacidade e preservação dos registros.

### Evolução posterior

- Resumo diário automático, após definição de horário e ativação.
- Dashboard privado e imagens dos resumos.
- Regras de classificação mais abrangentes e novas fontes de despesas, conforme necessidade.
- Dividir uma compra entre categorias, com valores que fechem exatamente no total, se surgir necessidade. Na primeira entrega cada compra tem uma categoria.

### Decisões que ainda precisam ser tomadas

- Horário do resumo diário.
- Referência desejada para os comparativos mensais de faturas, como mês do fechamento ou vencimento.
- Por quanto tempo manter os documentos originais.
- Quais compras e informações são efetivamente cobertas pelas notificações no aparelho de Rudolfo.
- Quando migrar os registros existentes e passar a considerar o novo controle como fonte principal.

Até a migração ser conferida, o controle atual não deve ser abandonado silenciosamente. A primeira versão estará pronta quando os três meios de entrada funcionarem e Rudolfo conseguir entender seus gastos, conferir a fatura e resolver dúvidas sem duplicação ou perda de informação.
