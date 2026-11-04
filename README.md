# mtrad

**English** | **[Português (Brasil)](README.pt-BR.md)**

Translates the texts of Minecraft mods (Fabric, and in testing Forge and NeoForge) into
Brazilian Portuguese, in separate stages that talk to each other only through files. It runs
locally and is free: the default engine is a local model served by Ollama
(`translategemma:4b`), and no account or API key is needed.

Status: **beta**. Text sources: the mods' language files (`en_us.json`), Patchouli books
(JSON) and FTB Quests (all versions: 1.20.1 and older keep the text inside the chapters,
1.21 and newer use language files).

> **Note on language.** The tool translates *into* pt-BR, and its own messages, menu and
> reports are in Portuguese. This README is a translation of `README.pt-BR.md`; the
> Portuguese file is the reference if the two differ.

Modpack texts in `kubejs/assets/<ns>/lang/en_us.json` (for example quests exported by the
FTB Quest Localizer, in `kubejs/assets/ftbquestlocalizer/lang`) are also read; the `FTBLang/`
folder (the Localizer's backups) is not.

Forge and NeoForge (in testing, only with hand-made instances): the mod name comes from
`META-INF/mods.toml` or `neoforge.mods.toml` (or `mcmod.info`, up to 1.12), and mods nested
inside mods (`META-INF/jarjar`) are read too.

## Installation

You do not need Go: download the ready-made program from the
[Releases](https://github.com/g0vane/mtrad/releases) page (use the latest version). Each
release has seven files and a `SHA256SUMS`. Pick yours:

| Your system | Download |
|---|---|
| Linux (regular PC, Intel or AMD) | `mtrad-linux-amd64` |
| Linux (ARM, such as Raspberry Pi 4/5) | `mtrad-linux-arm64` |
| Linux (64-bit RISC-V) | `mtrad-linux-riscv64` |
| macOS with an Intel chip | `mtrad-darwin-amd64` |
| macOS with an Apple chip (M1, M2, M3...) | `mtrad-darwin-arm64` |
| Windows 10 or 11 (regular PC, Intel or AMD) | `mtrad-windows-amd64.exe` |
| Windows 11 on ARM (Surface Pro X, Snapdragon Copilot+ PCs) | `mtrad-windows-arm64.exe` |

To know which Linux you have: `uname -m` shows `x86_64` (amd64), `aarch64` (arm64) or `riscv64`. On
macOS, the Apple menu > "About This Mac" shows the chip.

### Linux and macOS

    cd ~/Downloads
    sha256sum -c SHA256SUMS --ignore-missing     # on macOS: shasum -a 256 -c SHA256SUMS --ignore-missing
    chmod +x mtrad-linux-amd64                   # use the name you downloaded
    mkdir -p ~/.local/bin
    mv mtrad-linux-amd64 ~/.local/bin/mtrad
    mtrad --version

If the last command says "command not found", `~/.local/bin` is not in your `PATH`. Check with
`echo $PATH` and, if it is missing, add the line `export PATH="$HOME/.local/bin:$PATH"` to
`~/.bashrc` (or `~/.zshrc` on macOS) and open a new terminal.

### Windows

1. Create a folder, for example `C:\mtrad`, and put the `.exe` you downloaded there
   (`mtrad-windows-amd64.exe` or `mtrad-windows-arm64.exe`), renamed to `mtrad.exe`.
2. To check the file, in PowerShell: `Get-FileHash .\mtrad.exe -Algorithm SHA256` and compare
   it with the line of the file you downloaded in `SHA256SUMS` (the hash must be equal).
3. To use it from any folder, add `C:\mtrad` to the `PATH`: Start > "Edit the system
   environment variables" > Environment Variables > the user's `Path` > New.
4. Open a new PowerShell and run `mtrad --version`.

The menu (`mtrad ui`) reads keys with the `stty` command, so it only works on Linux and
macOS; on Windows use the commands (`mtrad run ...`).

### The binaries are not signed

The programs in the releases have no digital signature (that costs money and the project is
free). So the system may warn you the first time. The program is the same one built from the
open source code in this repository; if you prefer, build it yourself (next section).

- **Windows (SmartScreen):** "Windows protected your PC" appears. Click "More info" and then
  "Run anyway". Running from PowerShell usually shows no warning. If the file is marked as
  downloaded from the internet, PowerShell clears it with `Unblock-File .\mtrad.exe`.
- **macOS (Gatekeeper):** "cannot be opened because the developer cannot be verified" appears,
  or the terminal answers `killed`. Remove the quarantine mark from the file:
  `xattr -d com.apple.quarantine ~/.local/bin/mtrad`. Another way: try to open it once, then
  go to System Settings > Privacy & Security and click "Open Anyway".
- **Linux:** there is no block; only the execute permission is missing (`chmod +x`).

These workarounds come from the systems' own documentation. None was tested here on a real
Windows or macOS (see "What was NOT tested").

## Build

    go build -o mtrad ./cmd/mtrad        # Go standard library only (Go 1.24 or newer)
    go test ./...

## Quick start

    mtrad init -i ~/.var/app/org.prismlauncher.PrismLauncher/data/PrismLauncher/instances/MINE --engine llama
    mtrad doctor -c mtrad.json                                  # what is missing in the environment
    mtrad run -i INSTANCE_FOLDER -c mtrad.json --limit 50       # small test
    mtrad run -i INSTANCE_FOLDER -c mtrad.json --install        # everything, and copy FTB Quests

`init` accepts `--engine llama|ollama|gemini|openai|openrouter|custom|libre|ct2`. Ollama and the
cloud engines (`gemini`, `openai`, `openrouter`) need `--model`; mtrad does not guess cloud model
names. The `custom` engine talks to any server compatible with the OpenAI chat API:
`--base-url http://127.0.0.1:1234/v1 [--api-key-env VARIABLE_NAME]`. The API key lives only in the
environment: the file stores the variable **name**, and `init` rejects a key pasted in its place.
`--fallback gemini|openai|openrouter --fallback-model M` sets a cloud engine as the local one's
backup (texts the main engine fails on are sent to the cloud; without `--fallback`, nothing leaves
the computer). See `docs/HARDWARE_E_MOTORES.md` (Portuguese).
To test without an engine, configure an engine `{"type":"echo"}` (it returns the text with
"[pt] " in front).

Running `mtrad` with no arguments in a terminal opens a keyboard-driven menu (see
[Terminal interface](#terminal-interface-mtrad-ui)).

## Stages, one by one

    mtrad version -i INSTANCE_FOLDER -w work
    mtrad scan -w work
    mtrad extract -w work        # language files
    mtrad patchouli -w work      # Patchouli books
    mtrad ftbq -w work           # FTB Quests
    mtrad diff -w work           # merges all sources
    mtrad glossary --vanilla -w work [--tsv mine.tsv]
    mtrad protect -w work
    mtrad route -c mtrad.json -w work
    mtrad translate -c mtrad.json -w work        # can be stopped (Ctrl+C) and run again
    mtrad validate -w work                       # see work/report.md
    mtrad pack -w work           # resourcepacks/mtrad-pt_br.zip + work/out/ftbquests/
    mtrad install -w work        # copies FTB Quests to the game folder
    mtrad verify -w work         # checks the built pack against the originals
    mtrad activate -w work       # turns on the pack and pt_br language in options.txt (game closed)

Choose the sources with `"sources": ["lang", "patchouli", "ftbq"]` in the configuration file
(empty = all). A source that is turned off has its old files deleted from `work/` on the
next `run`.

To translate only some mods (large modpacks), use in the configuration file
`"only_mods": ["kubejs", "ftbquests"]` (only these) or `"skip_mods": ["chipped"]` (all but
these), by mod id; `kubejs` means the texts in `kubejs/assets`. What is already translated is
still reused, and what is left out stays in English. The cache keeps what was translated:
grow the list little by little and run again.

## Where things go

- Nothing is written into the .jar files.
- `resourcepacks/mtrad-pt_br.zip` in the instance: language texts and Patchouli books
  (`assets/<ns>/patchouli_books/<book>/pt_br/...`). Turn the pack on in the game menu
  (Options, Resource Packs) and choose the language Português (Brasil).
- FTB Quests is not a game resource: `pack` leaves the files in `work/out/ftbquests/` and
  `install` copies them to the instance. `install` never overwrites a different file without
  `--overwrite` (and then keeps the old one as `.bak`); `--dry-run` only shows what it would do.
  - 1.21 and newer: the file goes to `config/ftbquests/quests/lang/`.
  - 1.20.1 and older (text inside the chapters): `install` **rewrites the chapters**
    (`config/ftbquests/quests/chapters/*.snbt`), replacing each text with `{key}`; the keys
    and the texts (English and Portuguese) go in `assets/mtrad_ftbq/lang/` of the resource
    pack. The pack must stay on, otherwise quests show the key name. Lines in rich-text JSON
    (`{"text":...}`) are not translated.
- Patchouli books with `"i18n": true` (Botania, Hex Casting) keep their text in the language
  file; `patchouli` skips those books and `extract` already covers the text.

## Measuring speed: `mtrad bench`

    mtrad bench -c mtrad.json [--engine llm] [-n 12] [--words 120000]

Translates sample sentences on each configured engine and prints the first response (which
usually includes loading the model), words per second, time per request (median and p95) and how
long the remaining words would take (read from the work folder, or `--words`). Engines outside
this computer are skipped unless you pass `--allow-cloud` (sends sample sentences, may be billed).
It does not measure time to first token or GPU memory. During `mtrad translate` and on the menu's
translate screen, elapsed time, words per second and time remaining show in the progress bar.

## GPU, Intel Arc and llama.cpp

`mtrad doctor` measures GPU memory (NVIDIA via `nvidia-smi`, AMD via `/sys`, Mac via `sysctl`) and
suggests a local model. If `llama-server` (llama.cpp) is on your `PATH`, mtrad also runs
`llama-server --list-devices`, which measures Intel Arc, AMD and NVIDIA. If it finds a card but
cannot measure it (common with Intel Arc), it says so instead of reporting "CPU only". Then set the
memory by hand: `MTRAD_VRAM_GIB=16 mtrad doctor`. See `docs/HARDWARE_E_MOTORES.md`.

## Measuring an engine's quality

    mtrad evalset -w work -o evalwork -n 500
    mtrad protect -w evalwork && mtrad route -c mtrad.json -w evalwork
    mtrad translate -c mtrad.json -w evalwork && mtrad validate -w evalwork
    python3 tools/eval.py evalwork

## Folders

- `internal/schema`: types and reading/writing of the files (the only thing in common)
- `internal/{version,scan,extract,patchouli,ftbq,diff,glossary,vanilla,protect,route,translate,validate,pack,install,doctor,evalset}`: one module per folder
- `internal/{jsonx,jarx,relaxed}`: support libraries (tolerant JSON, jar reading, JSON5/SNBT)
- `tools/ct2worker.py`: neural translator on CPU (CTranslate2), a child process of `translate`
- `tools/eval.py`: scoring (sacreBLEU if installed)
- `cmd/prescript`: the extraction test pre-script

## Exit codes

0 ok, 1 error, 2 wrong usage, 3 finished with failed items or files that `install` did not
overwrite.

## What was NOT tested

No test ran the real game, llama-server, LibreTranslate or CTranslate2. The inline quests of
1.20.1 were checked against the FTB Quests and FTB Library code (`{key}` becomes `I18n.get`),
but no translated chapter was opened in the game. Forge modpacks were only tested with
hand-made instances. The test jars are hand-made or built from repositories; the real
Botania and Hex Casting books were read by the extractor (which is how we know they are
i18n), but no translated file was loaded by Patchouli or FTB Quests for real. Real Ollama
and GPU runs, the game opening the pack, and Windows (the build compiles, but the menu's key
reading needs `stty`) are also untested.

About the releases: the `release.yml` workflow ran on GitHub for v0.9.0 and its three jobs
passed; the Linux amd64 binary was downloaded from the release and checked. The other
binaries (macOS, Windows, Linux arm64, and the newer Linux riscv64 and Windows arm64) were
compiled but never run on a real machine, and the SmartScreen and Gatekeeper warnings
described in "Installation" were not seen here.

## Checking the pack: `mtrad verify`

`mtrad verify [--pack FILE.zip]` opens the `.zip` built by `pack` and checks it against the
originals: safe entry names (no `..`, `\`, absolute paths or duplicates), `pack.mcmeta` as
expected for the game version, every Patchouli document with the same structure as the
original (only the extracted strings may change), and language files with only keys that were
extracted. It exits with code 3 if it finds a problem. The same check is in the menu under
Maintenance > "Check the built pack". A Patchouli book showing "Loading error" or "No
entries" almost never comes from the pack: Patchouli fails the whole book if any single page
fails (for example a recipe that another mod or script removed).

## Resuming, failures and source text that changed

- Running `run` again continues where it stopped: what was already translated is not asked again.
- Text that validation rejected (`failed.jsonl`) is asked again from the engine on the next
  run, without using the cache. If the engine returns the same bad answer, the text stays
  rejected and stays in English in the pack.
- Each translation stores the hash of the source text. If a mod changes the English text but
  keeps the key, the text is translated again; in `validate`, a translation made for text that
  has since changed is "pending" (neither approved nor rejected) until it is redone.
- A text with a hand correction that is still valid (the original has not changed since) is
  never sent to the engine.
- If the engine puts `%s`/`%d` in another order (normal in Portuguese), `validate` replaces
  them with positional specifiers (`%2$d`, `%1$s`) and warns; changed `&` colour codes in a
  language file produce a warning.
- If a translation cannot be written to the cache, `run` warns and ends as incomplete
  (partial exit code).
- Hard-coded texts in KubeJS scripts (`.displayName('...')` in `startup_scripts`) do not go
  through a language file and the pack does not translate them; edit the script by hand (keep
  a copy first).

## Run log and `mtrad logs`

Every stage command (`scan`, `extract`, `translate`, `validate`, `pack`, `install`, `run`...)
writes a record in `work/logs/`:

- `run-YYYYMMDD-HHMMSS.log`: header (version, command, folder, machine), everything shown in
  the terminal (only the last progress line is kept) and a final line with duration and
  result. The contents of the configuration file never go in (only its path), so no API key
  reaches the disk.
- `requests-YYYYMMDD-HHMMSS.jsonl` (translation only): one line per engine request, with ID,
  time in ms, attempt, whether it came from the cache, whether it was redone, and the error.
  The text is not stored, only the ID.
- The 10 most recent runs are kept; older ones are deleted automatically.

`mtrad logs -w work-real` summarizes the last run: errors, lines of each stage, the most
common reasons in `errors.jsonl`, and request statistics (cache, errors, redone, mean/median/p95
time, the 5 slowest). `-n 1` looks at the one before, `-n 2` at the one before that.

## Cache inside the work folder

The cache lives in `work/cache/cache.jsonl`, with the rest of the work, and goes away only if
you delete the work folder. If an old cache exists at `<work>/../cache/cache.jsonl` and the new
one does not exist yet, it is copied automatically on the first run.

## Atomic writes

`pack` (the FTB Quests folder) and the Patchouli/FTB Quests extraction into `work/docs/` write
to a `.new` folder and swap it for the final one only at the end (`schema.SwapDir`). If
something fails midway, the previous output stays intact and the `.new` folder is removed. The
pack ZIP was already written to `.tmp` and renamed; files written this way are also flushed to
disk (`fsync`) before the rename.

## Single binary

`mtrad` builds into one static file (`CGO_ENABLED=0`), with no libraries or files beside it:
the `ct2` engine script (`ct2worker.py`) is embedded in the binary, and
`mtrad init --engine ct2` writes it to `tools/` if it is not there yet. You do **not** need to
keep the source folder to run the binary: put it anywhere (for example a folder in your
`PATH`). Configuration is read from `./mtrad.json`, otherwise `~/.config/mtrad/mtrad.json`.

    ./build.sh          # this machine only  -> dist/mtrad
    ./build.sh all      # Linux (amd64, arm64, riscv64), macOS (amd64, arm64) and Windows (amd64, arm64) + dist/SHA256SUMS
    VERSION=0.10 ./build.sh

`mtrad --version` (or `-V`) shows version, commit (if built in a git repository), Go version
and system. The version comes from `-X main.toolVersion=...` in `build.sh`.

### Publishing a version (for the maintainer)

The workflow `.github/workflows/release.yml` runs when a `vX.Y.Z` tag is pushed: it runs
gofmt, vet and `go test -race`, builds with `VERSION=X.Y.Z` (the tag without the `v`), checks
that `dist/` has exactly the 7 binaries and `SHA256SUMS` and that `mtrad --version` shows the
version, and creates the Release with those 6 files. The notes come from the commits since the
previous tag (no merges) and the fixed text at the end is in `.github/release-footer.md`. Only
the job that publishes has `contents: write`; the actions are pinned by commit SHA.

    git checkout main && git pull
    git tag -a v0.9.0 -m "mtrad 0.9.0"
    git push origin v0.9.0

If it goes wrong before publishing, delete the tag (`git push origin --delete v0.9.0` and
`git tag -d v0.9.0`), fix it and create it again. The workflow does not overwrite a release
that already exists: delete it on the Releases page before repeating the tag.

## Terminal interface (`mtrad ui`)

`mtrad` with no arguments, in a terminal, opens a keyboard-driven menu (↑ ↓ move, Enter
chooses, Space marks, Esc goes back, `/` filters lists, `i` switches the instance menu style,
`?` help). `mtrad ui` does the same and accepts options; with arguments `mtrad` remains the
usual command-line program. Everything the menu does has an equivalent command, shown on screen.

    mtrad                          # opens the menu (needs an interactive terminal)
    mtrad ui -c mtrad.json -w ~/mtrad-trabalho
    mtrad ui --demo                # fake data: nothing is executed or written

Flow: welcome; "Configure (setup)" in 6 steps (computer, engine, model, sources, work folder,
summary) writes `mtrad.json` (the previous one goes to `.bak`); "Select your instance" lists
the Prism Launcher instances (Flatpak or regular install) and opens the instance menu:
Translate (small test or full, pausing with Esc), Choose mods (writes `skip_mods`; mods with
repetitive names, like `<material> <shape>`, are marked "repetitive", with the estimated
minutes next to each), Failed (edit by hand, leave in English, apply the corrections, try
again), Result (where the pack is, and turn it on in `options.txt`), Install FTB Quests,
Logs and Maintenance (check the environment, check the built pack, remove the lock).

Where things are:

- Configuration: `./mtrad.json` if it exists, otherwise `~/.config/mtrad/mtrad.json`. Two
  fields are only for the interface: `work` (work folder) and `last_instance`.
- Work folder: `~/mtrad-trabalho` by default. Each instance uses a subfolder with its name; a
  folder that already holds one instance's work stays that instance's. The translation cache is
  shared (`<folder>/cache/cache.jsonl`).
- Pack: `<game folder>/resourcepacks/mtrad-pt_br.zip`, right where the game looks. "Activate"
  edits `options.txt` (keeping the original as `options.txt.mtrad.bak`; a second activation
  keeps `.bak.2` and never replaces the first copy) and refuses if the game is open.
- During a stage the output goes to the log (`logs/`), not to the screen, so the menu is not
  messed up; the summary appears at the end. If the engine fails, the screen shows the reason
  (count + message from `errors.jsonl`).
- The interface never runs `sudo`, never deletes the work folder or the cache, and does not
  store API keys.

The logic is in `internal/ui`, which only talks to the `Backend` interface (`backend.go`).
There are two implementations: `Demo` (`demo.go`, fake data, used by `--demo` and the tests)
and the real one in `cmd/mtrad/uibackend.go`, which calls the `mtrad` stages. Keys are read
with the `stty` command (`term.go`), with no extra library, on Linux and macOS. The program
uses the alternate screen and draws no background colour; `NO_COLOR=1` turns colours off. The
terminal mode is `-icanon -echo -isig`, never `raw` (`raw` turns off the `\n` to `\r\n`
conversion and the screen comes out staircased). A small window (under 24 lines or 66
columns) uses compact mode. The full design is in `docs/INTERFACE.md` (in Portuguese).

`cmd/mtrad-ui` (the old prototype, fake data only) still builds, but was replaced by
`mtrad ui --demo`.

## Alternative visual prototype (`mtrad-prototype`)

`cmd/mtrad-prototype` (received from the user on 2026-10-28) is a separate demo to compare
three visual styles (Classic, Panels and Dashboard) with arrow-key navigation and the menu
Overview / Instances / Translate / Review failures / Results / History / Settings. It is
visual only: it does not connect to the backend. See `cmd/mtrad-prototype/README.md`.

## License

`mtrad` is free software under the GNU General Public License version 3 (file `LICENSE`): you
may use, study, modify and redistribute it, and anyone who distributes a modified version must
keep the source open under the same license. The license covers the program, not the
translation packs it generates. The program does not distribute mod jars or translation
models; each has its author's own license.

See the full license text in the [LICENSE](LICENSE) file.
