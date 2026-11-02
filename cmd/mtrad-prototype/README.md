# Protótipo visual do mtrad

Este comando é uma demonstração **somente da interface** proposta para o mtrad.
Ele não conecta ao tradutor, não consulta o Prism Launcher, não grava configurações,
não traduz, não instala arquivos e não apaga nada. Os números e instâncias são fictícios.

## Executar

Na raiz do projeto, com Go instalado:

```sh
go run ./cmd/mtrad-prototype
```

Para compilar:

```sh
go build -o mtrad-interface-prototype ./cmd/mtrad-prototype
./mtrad-interface-prototype
```

O protótipo usa apenas a biblioteca padrão do Go e o comando `stty` do Linux para ler
as setas. Foi pensado para terminal Linux interativo.

## Teclas

- `↑` / `↓`: navegar
- `Enter`: abrir/selecionar
- `Esc`: voltar
- `i`: trocar o estilo em qualquer tela
- `?`: ajuda
- `q` ou `Ctrl+C`: sair

## Estilos demonstrados

- **Clássica:** menu vertical e simples.
- **Painéis:** navegação lateral e conteúdo ao lado.
- **Dashboard:** resumo de estado e ações.

O seletor de estilo é a primeira tela. A escolha é apenas temporária nesta demonstração;
não é gravada em configuração.

## Transparência

O código não emite sequências ANSI de cor de fundo: estiliza apenas o texto. Isso permite
que o terminal use o próprio fundo, incluindo transparência configurada no emulador.
A aparência exata depende do terminal e de como ele trata células vazias e limpeza de tela.
