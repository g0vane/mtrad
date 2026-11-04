# Hardware, modelos, nuvem e velocidade

Este documento registra a alteração que acrescentou: detecção do computador,
sugestão de modelos locais, motores de nuvem, `mtrad bench` e o progresso com
palavras por segundo. Tudo em Go, só com a biblioteca padrão.

## O que mudou

| Parte | Onde | O que faz |
|---|---|---|
| Hardware | `internal/hardware` | Acha a placa de vídeo (NVIDIA por `nvidia-smi`, AMD por `/sys/class/drm`, Mac por `sysctl`), a RAM e os núcleos. Calcula a memória utilizável da placa: total − em uso − 1,2 GiB de folga. |
| Catálogo | `internal/hardware/catalog.go` | Tabela de modelos do Ollama com tamanho, data de conferência e função `Recommend`. |
| Doctor e setup | `internal/doctor/hardware.go`, `internal/ui` | Mostra o computador e o modelo sugerido; avisa se o modelo da configuração não cabe. No passo 3 do setup lista os instalados, os do catálogo que rodam aqui e "Digitar outro nome…". |
| Nuvem | `cmd/mtrad/init.go` | `--engine openai`, `openrouter` e `custom`; `--fallback` opcional. |
| Setup do menu | `cmd/mtrad/uibackend.go` | Trocar o modelo no setup não apaga mais o `fallback`, `workers` e limites que você ajustou à mão. |
| Medidor | `internal/translate/meter.go` | Conta palavras do cache e do motor; calcula palavras por segundo e o tempo que falta. |
| Progresso | `internal/ui`, `cmd/mtrad/progress.go` | Tempo decorrido, palavras por segundo e tempo que falta na barra e no terminal. |
| Bench | `internal/bench`, `cmd/mtrad/bench.go` | `mtrad bench`: velocidade de cada motor com frases de exemplo e estimativa para o seu modpack. |

## Decisões e limites (leia antes de confiar nos números)

- **Palavra** é um pedaço separado por espaço com ao menos uma letra ou número. As marcas `⟦n⟧` não contam.
- **Palavras por segundo** = palavras que o **motor** traduziu ÷ tempo decorrido. O cache não entra, para não inflar o número.
- **Tempo que falta** só aparece depois de 3 s e com palavras traduzidas pelo motor. Ele supõe que o ritmo se mantém; textos que falham não diminuem o que falta.
- **Memória unificada (Apple Silicon)**: conto 2/3 do total para o modelo. É uma estimativa prudente minha, não um número do sistema.
- **Várias placas**: conto só a melhor, sem supor divisão do modelo entre elas.
- **Cabe ou não** compara o tamanho do download do Ollama com a memória livre. O contexto e o cache de atenção usam mais memória que isso; a folga de 1,2 GiB cobre uma parte, não tudo.
- **Intel Arc e placas "achadas, mas não medidas"**: o driver da Intel (i915/xe) não mostra o tamanho da memória em `/sys`. O mtrad vê a placa pelo fabricante PCI (`0x8086`), mas não sabe quanta memória ela tem. Nesse caso ele **não diz "só CPU"**: diz que achou a placa e não conseguiu medir, sugere o menor modelo e pede uma de duas coisas (abaixo).
- **llama.cpp mede a placa**: se `llama-server` está no `PATH`, o mtrad roda `llama-server --list-devices` (8 s no máximo) e lê linhas como `  Vulkan0: NOME (TOTAL MiB, FREE MiB free)`. Isso mede Arc, AMD e NVIDIA sem depender do `/sys`. O motor `llama` (`type: openai`, `http://127.0.0.1:8080/v1`) é o caminho para Arc, via SYCL ou Vulkan.
- **`MTRAD_VRAM_GIB`**: define à mão a memória da placa (ex.: `MTRAD_VRAM_GIB=16`; aceita vírgula, de 0 a 1024). Substitui qualquer medida e vira uma placa `manual`. Valor inválido é **ignorado** e o `doctor` mostra um **aviso**.
- **Ollama e Arc**: as fontes que li se contradizem (veja abaixo). Não afirmo que o Ollama usa a Arc; o `doctor` só mede o que o computador oferece.
- **`bench` não mede** o tempo até a primeira palavra (o mtrad pede a resposta inteira, sem streaming) nem o pico de memória da placa.
- **`bench` e nuvem**: motores fora do loopback (`127.0.0.1`, `localhost`, `::1`) são pulados sem `--allow-cloud`. Um servidor na rede de casa também conta como "fora".
- **Chave de API**: o arquivo guarda só o nome da variável. `init --api-key-env` recusa texto que não pareça um nome de variável e não repete a chave na mensagem de erro.
- **`rpm`/`rpd`**: não pus limites nos motores `openai` e `openrouter` porque não conheço os limites da sua conta. Ajuste no arquivo se precisar.

## O que não entrou

- Python, `pydantic`, PDF/SRT e divisão de textos longos: são outro produto.
- `confidence_score` pedido ao modelo: é a nota que o próprio modelo dá a si mesmo.
- API pública gratuita como último recurso automático: fere a regra de só usar motores que você configurou.
- **DeepL**: fica para depois (combinado). Exige um motor novo e precisa de teste com chave real, para ver se ele preserva as marcas `⟦n⟧`.

## Pesquisa e fontes

Abriram (conferidos em 2026-11-03):

- `ollama.com/library/translategemma/tags`: tamanhos 4b 3,3 GB, 12b 8,1 GB, 27b 17 GB; contexto de 128K.
- `ollama.com/library/translategemma:4b`: a tabela de idiomas inclui `pt`, `pt-BR` e `pt-PT`.
- `ollama.com/library/gemma3`: 4b 3,4 GB, 12b 8,2 GB, 27b 17 GB.
- `infoq.com/news/2026/01/google-translategemma-models`: tamanhos 4B, 12B e 27B; o 12B reportou menos erros que o Gemma 3 de 27B (números do Google, não medidos aqui).
- `openrouter.ai/docs/quick-start`: base `https://openrouter.ai/api/v1`, chave no cabeçalho `Authorization: Bearer`.
- `github.com/ainoya/openai-java-generated-client/blob/main/docs/ChatApi.md`: base `https://api.openai.com/v1`, rota `POST /chat/completions`, chave como bearer. É um cliente gerado de terceiros, não a documentação oficial da OpenAI; confira a oficial antes de depender disso.

Para Intel Arc e llama.cpp (conferidos em 2026-11-04):

- Guia `steelph0enix` (llama.cpp): o llama.cpp roda em Intel por SYCL e por Vulkan. Dele e da discussão #26683 do llama.cpp tirei duas **saídas reais** de `--list-devices` no formato `  Vulkan0: NOME (TOTAL MiB, FREE MiB free)`; estão em `internal/hardware/intel_test.go`.
- Página de manual do Debian do `llama-server`: confirma que `--list-devices` existe.
- Issue #1283 do `tasksmack`: o i915/xe não expõe o tamanho da VRAM no sysfs; medir de verdade exige ioctls do DRM (não implementei).
- Guia `aliteq` e uma conversa na Intel Community: **se contradizem** sobre o Ollama com Arc. Um diz que o Ollama usa Vulkan sozinho e que o IPEX-LLM foi arquivado em janeiro de 2026; a outra relata só CPU no Windows, sem correção confirmada.

Não abriram (bloqueados pela lista de sites permitidos): o README do llama.cpp, `ollama.com/library/translategemma`, `ollama.com/library/qwen2.5`, `ollama.com/library/qwen3`. Por isso `qwen` e `llama` **não** estão no catálogo: só entram modelos cujo tamanho foi lido numa página.

## O que foi testado e o que não foi

Testado: `gofmt`, `go vet` e `go test ./... -race` passam em todos os pacotes; o binário compila (`CGO_ENABLED=0`) para linux/amd64, linux/arm64, windows/amd64 e darwin/arm64. Rodei `mtrad init --engine custom` e `mtrad bench` de verdade contra um servidor falso compatível com a API de chat da OpenAI, e `mtrad doctor` neste computador (sem placa de vídeo, 7,8 GiB de RAM).

Também testado com um `llama-server` falso no `PATH` que imprime o texto real do guia: o `doctor` mediu a RX 7900 XT (20,0 GiB, 18,8 GiB úteis) e sugeriu `translategemma:27b`; com `MTRAD_VRAM_GIB=12` sugeriu o 12b; com `MTRAD_VRAM_GIB=doze` ignorou e avisou.

**Não testado:**

- **Intel Arc de verdade.** O formato da linha do `--list-devices` com SYCL (`SYCL0: ...`) **não foi visto em saída real**; o teste usa um formato que eu supus e diz isso no nome (`llamaArcPresumido`). Se a sua Arc aparecer diferente, o mtrad a mostra como "achada, mas não medida" e `MTRAD_VRAM_GIB` resolve. Rode `mtrad doctor` e `llama-server --list-devices` e me mande a saída.
- Placa de vídeo real: a leitura de `nvidia-smi`, `/sys/class/drm` e `sysctl` foi testada só com saídas simuladas. O formato real pode diferir em algum driver.
- Ollama real, e as APIs de OpenAI, OpenRouter e Gemini: nenhuma chamada foi feita a esses serviços. O que vale para eles vem das páginas acima.
- Os binários de Windows e macOS foram compilados, não executados.
- O menu interativo foi testado com teclas simuladas, não num terminal de verdade.
- Os tamanhos do catálogo mudam quando o Ollama republica um modelo: refaça a conferência (`CatalogChecked`) de tempos em tempos.
