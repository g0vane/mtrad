# mtrad

Traduz os textos de mods do Minecraft (Fabric, e em teste Forge e NeoForge) para
português do Brasil, em etapas separadas que conversam só por arquivos. Veja o documento
de pesquisa, seções 19 a 26, para a teoria e para o que foi testado.

Versão: **beta**. Fontes de texto: arquivos de idioma dos mods (`en_us.json`), livros do
Patchouli (JSON) e missões do FTB Quests (todas as versões: 1.20.1 e antes com o texto
dentro dos capítulos, 1.21 em diante com arquivos de idioma).

Textos próprios de modpacks em `kubejs/assets/<ns>/lang/en_us.json` (por exemplo as
missões exportadas pelo FTB Quest Localizer, em `kubejs/assets/ftbquestlocalizer/lang`)
também são lidos; a pasta `FTBLang/` (cópias de segurança do Localizer) não.

Forge e NeoForge (em teste, só com instâncias de mentira): o nome do mod vem de
`META-INF/mods.toml` ou `neoforge.mods.toml` (ou `mcmod.info`, até 1.12) e os mods dentro
de mods (`META-INF/jarjar`) também são lidos.

## Compilar

    go build -o mtrad ./cmd/mtrad        # só biblioteca padrão do Go (1.24 ou mais)
    go test ./...

## Começo rápido

    mtrad init -i ~/.var/app/org.prismlauncher.PrismLauncher/data/PrismLauncher/instances/MINHA --engine llama
    mtrad doctor -c mtrad.json                                  # o que falta no ambiente
    mtrad run -i PASTA_DA_INSTANCIA -c mtrad.json --limit 50    # teste pequeno
    mtrad run -i PASTA_DA_INSTANCIA -c mtrad.json --install     # tudo, e copia o FTB Quests

`init` aceita `--engine llama|ollama|gemini|libre|ct2` (ollama e gemini pedem `--model`).
Para testar sem motor: configure um motor `{"type":"echo"}` (devolve o texto com "[pt] "
na frente).

## Etapas, uma a uma

    mtrad version -i PASTA_DA_INSTANCIA -w work
    mtrad scan -w work
    mtrad extract -w work        # arquivos de idioma
    mtrad patchouli -w work      # livros do Patchouli
    mtrad ftbq -w work           # missões do FTB Quests
    mtrad diff -w work           # junta todas as fontes
    mtrad glossary --vanilla -w work [--tsv meu.tsv]
    mtrad protect -w work
    mtrad route -c mtrad.json -w work
    mtrad translate -c mtrad.json -w work        # pode parar (Ctrl+C) e rodar de novo
    mtrad validate -w work                       # veja work/report.md
    mtrad pack -w work           # resourcepacks/mtrad-pt_br.zip + work/out/ftbquests/
    mtrad install -w work        # copia o FTB Quests para a pasta do jogo
    mtrad verify -w work         # confere o pacote montado contra os originais
    mtrad activate -w work       # liga o pacote e o idioma pt_br no options.txt (jogo fechado)

Escolha as fontes com `"sources": ["lang", "patchouli", "ftbq"]` no arquivo de
configuração (vazio = todas). Uma fonte desligada tem os arquivos antigos apagados do
`work/` na execução seguinte do `run`.

Para traduzir só alguns mods (modpacks grandes), use no arquivo de configuração
`"only_mods": ["kubejs", "ftbquests"]` (só esses) ou `"skip_mods": ["chipped"]` (todos menos
esses), pelo id do mod; `kubejs` são os textos de `kubejs/assets`. O que já tem tradução
continua sendo reaproveitado, e o que fica de fora continua em inglês. O cache guarda o
que já foi traduzido: aumente a lista aos poucos e rode de novo.

## Onde cada coisa vai

- Nada é escrito nos .jar.
- `resourcepacks/mtrad-pt_br.zip` na instância: textos de idioma e livros do Patchouli
  (`assets/<ns>/patchouli_books/<livro>/pt_br/...`). Ative o pacote no menu do jogo
  (Opções, Pacotes de recursos) e escolha o idioma Português (Brasil).
- FTB Quests não é recurso do jogo: o `pack` deixa os arquivos em `work/out/ftbquests/` e
  o `install` copia para a instância. O `install` nunca sobrescreve um arquivo diferente
  sem `--overwrite` (e então guarda o antigo em `.bak`); `--dry-run` só mostra o que faria.
  - 1.21 em diante: o arquivo vai para `config/ftbquests/quests/lang/`.
  - 1.20.1 e antes (texto dentro dos capítulos): o `install` **reescreve os capítulos**
    (`config/ftbquests/quests/chapters/*.snbt`), trocando cada texto por `{chave}`; as
    chaves e os textos (inglês e português) ficam em `assets/mtrad_ftbq/lang/` do
    resource pack. O pacote precisa continuar ligado, senão as missões mostram o nome da
    chave. Linhas em JSON de texto rico (`{"text":...}`) não são traduzidas.
- Livros do Patchouli com `"i18n": true` (Botania, Hex Casting) guardam o texto no arquivo
  de idioma; o `patchouli` pula esses livros e o `extract` já cobre o texto.

## Medir um motor

    mtrad evalset -w work -o evalwork -n 500
    mtrad protect -w evalwork && mtrad route -c mtrad.json -w evalwork
    mtrad translate -c mtrad.json -w evalwork && mtrad validate -w evalwork
    python3 tools/eval.py evalwork

## Pastas

- `internal/schema`: tipos e leitura/gravação dos arquivos (único ponto em comum)
- `internal/{version,scan,extract,patchouli,ftbq,diff,glossary,vanilla,protect,route,translate,validate,pack,install,doctor,evalset}`: um módulo por pasta
- `internal/{jsonx,jarx,relaxed}`: bibliotecas de apoio (JSON tolerante, leitura de jars, JSON5/SNBT)
- `tools/ct2worker.py`: tradutor neural em CPU (CTranslate2), processo filho do `translate`
- `tools/eval.py`: pontuação (sacreBLEU se instalado)
- `cmd/prescript`: o pré-script de teste da extração

## Códigos de saída

0 ok, 1 erro, 2 uso errado, 3 terminou com itens falhos ou arquivos que o `install` não
sobrescreveu.

## O que NÃO foi testado

Nenhum teste rodou o jogo, o llama-server, o LibreTranslate nem o CTranslate2 reais. As
missões inline do 1.20.1 foram conferidas no código do FTB Quests e do FTB Library
(`{chave}` vira `I18n.get`), mas nenhum capítulo traduzido foi aberto no jogo. Os modpacks
Forge só foram testados com instâncias montadas à mão. Os jars dos testes são montados a mão ou a partir dos
repositórios; os livros reais do Botania e do Hex Casting foram lidos pelo extrator (por
isso sabemos que são i18n), mas nenhum arquivo traduzido foi carregado pelo Patchouli nem
pelo FTB Quests de verdade. As partes sem confirmação (formato exato do SNBT que o FTB
Quests lê, marcas `&a` e `{image:...}`, layout de `assets/` e `libraries/` do Prism, nome
do jar do cliente) estão listadas na seção 24 do documento.

## Conferir o pacote: `mtrad verify`

`mtrad verify [--pack ARQ.zip]` abre o `.zip` que o `pack` montou e confere contra os originais: nomes de entrada seguros (sem `..`, `\`, caminho absoluto, duplicados), `pack.mcmeta` igual ao esperado para a versão do jogo, cada documento do Patchouli com a mesma estrutura do original (só as strings extraídas podem mudar) e arquivos de idioma só com chaves que foram extraídas. Termina com código 3 se achar algum problema. O mesmo conferidor está no menu em Manutenção > "Conferir o pacote montado". Um "Erro de carregamento" ou "Sem Entradas" num livro do Patchouli quase nunca vem do pacote; veja `docs/` (seção 39 do documento de pesquisa) para os motivos mais comuns.

## Retomar, reprovados e texto de origem que mudou

- Rodar `run` de novo continua de onde parou: o que já foi traduzido não é pedido outra vez.
- Texto que a validação reprovou (`failed.jsonl`) é pedido de novo ao motor na próxima execução, sem usar o cache. Se o motor devolver a mesma resposta ruim, o texto continua reprovado e fica em inglês no pacote.
- Cada tradução guarda o hash do texto de origem. Se o mod mudar o texto em inglês mantendo a chave, o texto é traduzido de novo; no `validate`, uma tradução feita para um texto que já mudou fica "pendente" (não vale nem como aprovada nem como reprovada) até ser refeita.
- Texto com correção feita à mão que ainda vale (o original não mudou desde a correção) nunca é enviado ao motor.
- Se o motor colocar `%s`/`%d` em outra ordem (normal em português), o `validate` troca por especificadores com posição (`%2$d`, `%1$s`) e avisa; códigos de cor com `&` que mudam num arquivo de idioma geram aviso.
- Se uma tradução não puder ser gravada no cache, o `run` avisa e termina como incompleto (código de saída parcial).
- Textos fixos em scripts do KubeJS (`.displayName('...')` em `startup_scripts`) não passam por arquivo de idioma e o pacote não os traduz; edite o script à mão (guarde uma cópia antes).

## Registro de execuções e `mtrad logs`

Cada comando de etapa (`scan`, `extract`, `translate`, `validate`, `pack`, `install`, `run`...) grava um registro em `work/logs/`:

- `run-AAAAMMDD-HHMMSS.log`: cabeçalho (versão, comando, pasta, máquina), tudo o que apareceu no terminal (do progresso fica só a última linha) e uma linha final com duração e resultado. O conteúdo do arquivo de configuração nunca entra (só o caminho), para não levar chave de API para o disco.
- `requests-AAAAMMDD-HHMMSS.jsonl` (só na tradução): uma linha por pedido ao motor, com ID, tempo em ms, tentativa, se veio do cache, se foi refeito e o erro. O texto não é gravado, só o ID.
- Ficam as 10 execuções mais recentes; as antigas são apagadas sozinhas.

`mtrad logs -w work-real` resume a última execução: erros, linhas de cada etapa, motivos mais comuns em `errors.jsonl` e estatísticas dos pedidos (cache, erros, refeitos, tempo médio/mediana/p95, os 5 mais lentos). `-n 1` olha a penúltima, `-n 2` a antepenúltima.

## Cache dentro da pasta de trabalho

O cache fica em `work/cache/cache.jsonl`, junto do resto do trabalho, e some só se você apagar a pasta de trabalho. Se existir um cache antigo em `<work>/../cache/cache.jsonl` e ainda não houver o novo, ele é copiado automaticamente na primeira execução.

## Gravação atômica

`pack` (pasta FTB Quests) e a extração de Patchouli/FTB Quests em `work/docs/` escrevem numa pasta `.new` e só trocam pela definitiva no fim (`schema.SwapDir`). Se algo falhar no meio, a saída anterior continua intacta e a pasta `.new` é removida. O ZIP do pacote já era escrito em `.tmp` e renomeado.

## Binário único

O `mtrad` compila num arquivo estático (`CGO_ENABLED=0`), sem bibliotecas nem arquivos ao lado: o script do motor `ct2` (`ct2worker.py`) vai dentro do binário e o `mtrad init --engine ct2` o grava em `tools/` se ainda não existir.

    ./build.sh          # só esta máquina  -> dist/mtrad
    ./build.sh all      # Linux, macOS e Windows (amd64/arm64) + dist/SHA256SUMS
    VERSION=0.10 ./build.sh

`mtrad --version` (ou `-V`) mostra versão, commit (se compilado num repositório git), versão do Go e sistema. A versão vem do `-X main.toolVersion=...` do `build.sh`.

## Interface no terminal (`mtrad ui`)

`mtrad` sem argumentos, num terminal, abre um menu controlado por teclas (↑ ↓ movem, Enter escolhe, Espaço marca, Esc volta, `/` filtra listas, `i` troca o estilo do menu da instância, `?` ajuda). `mtrad ui` faz o mesmo e aceita opções; com argumentos o `mtrad` continua sendo o programa de linha de comando de sempre. Tudo o que o menu faz tem um comando equivalente, mostrado na tela.

    mtrad                          # abre o menu (precisa de terminal interativo)
    mtrad ui -c mtrad.json -w ~/mtrad-trabalho
    mtrad ui --demo                # dados falsos: nada é executado nem gravado

Fluxo: boas-vindas; "Configurar (setup)" em 6 passos (computador, motor, modelo, fontes, pasta de trabalho, resumo) grava o `mtrad.json` (o anterior vai para `.bak`); "Selecionar sua instância" lista as instâncias do Prism (Flatpak ou instalação normal) e abre o menu da instância: Traduzir (teste pequeno ou completo, com pausa em Esc), Escolher mods (grava `skip_mods`; mods de nomes repetitivos, como `<material> <forma>`, vêm marcados "repetitivo"), Reprovados (editar à mão, deixar em inglês, aplicar as correções, tentar de novo), Resultado (onde está o pacote e ativar no `options.txt`), Instalar missões do FTB Quests, Registros e Manutenção (verificar o ambiente, conferir o pacote montado, remover trava). A lista de mods mostra, ao lado de cada mod "repetitivo", quantos minutos a tradução dele deve levar na máquina.

Onde as coisas ficam:

- Configuração: `./mtrad.json` se existir, senão `~/.config/mtrad/mtrad.json`. Dois campos são só da interface: `work` (pasta de trabalho) e `last_instance`.
- Pasta de trabalho: `~/mtrad-trabalho` por padrão. Cada instância usa uma subpasta com o nome dela; uma pasta que já tem o trabalho de uma instância continua sendo dessa instância. O cache de traduções é compartilhado (`<pasta>/cache/cache.jsonl`).
- Pacote: `<pasta do jogo>/resourcepacks/mtrad-pt_br.zip`, direto onde o jogo procura. "Ativar" mexe no `options.txt` (guardando o original em `options.txt.mtrad.bak`; uma segunda ativação guarda `.bak.2` e nunca troca a primeira cópia) e se recusa se o jogo estiver aberto.
- Durante uma etapa a saída vai para o registro (`logs/`), não para a tela, para não bagunçar o menu; o resumo aparece ao fim. Se o motor falhar, a tela mostra o motivo (contagem + mensagem do `errors.jsonl`).
- A interface nunca roda `sudo`, nunca apaga a pasta de trabalho nem o cache e não guarda chave de API.

A lógica fica em `internal/ui`, que só fala com a interface `Backend` (`backend.go`). Há duas implementações: `Demo` (`demo.go`, dados falsos, usada em `--demo` e nos testes) e a de verdade em `cmd/mtrad/uibackend.go`, que chama as etapas do `mtrad`. As teclas são lidas com o comando `stty` (`term.go`), sem biblioteca extra, em Linux e macOS. O programa usa a tela alternativa e não desenha cor de fundo; `NO_COLOR=1` desliga as cores. O modo do terminal é `-icanon -echo -isig`, nunca `raw` (o `raw` desliga a conversão de `\n` em `\r\n` e a tela sai em escada). Janela pequena (menos de 24 linhas ou 66 colunas) usa o modo compacto. O desenho completo está em `docs/INTERFACE.md`.

`cmd/mtrad-ui` (o protótipo antigo, só com dados falsos) continua compilando, mas foi substituído por `mtrad ui --demo`.

## Protótipo visual alternativo (`mtrad-prototype`)

`cmd/mtrad-prototype` (recebido do usuário em 28/10/2026) é uma demonstração separada para comparar três estilos visuais (Clássica, Painéis e Dashboard) com navegação por setas e o menu Visão geral / Instâncias / Traduzir / Revisar falhas / Resultados / Histórico / Configurações. É somente visual: não conecta ao backend. Ver `cmd/mtrad-prototype/README.md`. Correção feita no código recebido: o terminal era posto em `stty raw`, que desliga a conversão de `\n` em `\r\n` (a tela sai em escada); agora usa `-icanon -echo -isig`.

## Licença

O `mtrad` é software livre sob a GNU General Public License versão 3 (arquivo `LICENSE`): você pode usar, estudar, modificar e redistribuir, e quem distribuir uma versão modificada deve manter o código aberto sob a mesma licença. A licença vale para o programa, não para os pacotes de tradução que ele gera. O programa não distribui jars de mods nem modelos de tradução; cada um tem a licença do seu autor.
