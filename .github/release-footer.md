---

## Como baixar e usar

Baixe **um** dos arquivos abaixo, o que combina com o seu sistema, e também o `SHA256SUMS`.

| Seu sistema | Arquivo |
|---|---|
| Linux (PC comum, Intel ou AMD) | `mtrad-linux-amd64` |
| Linux (ARM, como Raspberry Pi 4/5) | `mtrad-linux-arm64` |
| Linux (RISC-V de 64 bits) | `mtrad-linux-riscv64` |
| macOS com chip Intel | `mtrad-darwin-amd64` |
| macOS com chip Apple (M1, M2, M3...) | `mtrad-darwin-arm64` |
| Windows 10 ou 11 (PC comum, Intel ou AMD) | `mtrad-windows-amd64.exe` |
| Windows 11 em ARM (Surface Pro X, Copilot+ PC com Snapdragon) | `mtrad-windows-arm64.exe` |

### Conferir o arquivo (recomendado)

O `SHA256SUMS` guarda a "impressão digital" de cada arquivo. Se a conta bater, o arquivo veio inteiro e sem alteração.

- Linux: `sha256sum -c SHA256SUMS --ignore-missing`
- macOS: `shasum -a 256 -c SHA256SUMS --ignore-missing`
- Windows (PowerShell): `Get-FileHash .\mtrad-windows-amd64.exe -Algorithm SHA256`, e compare o resultado com a linha do mesmo arquivo no `SHA256SUMS`.

### Dar permissão de execução (Linux e macOS)

    chmod +x mtrad-linux-amd64
    ./mtrad-linux-amd64 --version

Para usar só como `mtrad`, renomeie o arquivo e coloque numa pasta do `PATH` (veja a seção "Instalação" do README).

### Aviso: os binários não são assinados

Windows (SmartScreen) e macOS (Gatekeeper) podem bloquear o programa na primeira vez. Isso é esperado e o README explica como liberar. O código-fonte é aberto (GPL-3.0) e dá para compilar você mesmo com `./build.sh`.
