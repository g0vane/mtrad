# Code review brief: `mtrad`

You are reviewing the source code of **mtrad**, a command-line tool (with an interactive terminal menu) that translates Minecraft mods and modpacks into Brazilian Portuguese (pt-BR) using a local LLM. You have the whole source tree. Read the code, build it, run the tests, and report **real defects**. Do not rewrite the project and do not report style preferences.

Your reply is going to be pasted into another session that will fix the code, so every finding must be specific enough to act on without asking you anything.

---

## 1. What the program does

Input: a Minecraft instance folder (Prism Launcher layout: `<instance>/minecraft/mods/*.jar`, `config/`, `kubejs/`, `resourcepacks/`, `options.txt`).
Output: a resource pack `mtrad-pt_br.zip` (and, optionally, translated FTB Quests files) that makes the game show Portuguese text.

Text sources:

- `assets/<ns>/lang/en_us.json` (and legacy `.lang`) inside mod jars (Fabric, Forge, NeoForge, jar-in-jar included).
- Patchouli guide books (`data/<ns>/patchouli_books/...` JSON).
- FTB Quests (`.snbt` chapters for 1.20.1 and older, language files for 1.21+).
- `kubejs/assets/<ns>/lang/en_us.json`.

Translation engines (all optional, all configured in `mtrad.json`): an OpenAI-compatible HTTP server (Ollama, llama.cpp, Gemini's OpenAI endpoint), LibreTranslate, and a `worker` engine that runs a user-configured command speaking JSON lines (an embedded `ct2worker.py` is provided).

## 2. Constraints that are part of the design

- **Go 1.24, standard library only** (`go.mod` has no dependencies), no cgo, static binary, Linux/macOS/Windows build (`./build.sh all`). The UI reads keys through the `stty` command (Linux/macOS only).
- **Free and local-first.** No telemetry, no network calls except to the engine URLs the user configured (and to the local Ollama for status, `127.0.0.1:11434`).
- **Stages talk only through files** in a work directory. Each stage can be run alone (`mtrad scan`, `extract`, `patchouli`, `ftbq`, `diff`, `protect`, `route`, `translate`, `validate`, `pack`, `install`, `activate`, ...); `mtrad run` chains them; `mtrad ui` (or `mtrad` with no arguments on a terminal) is the menu.
- Comments and user-facing strings are in **Brazilian Portuguese**. Identifiers are a mix. That is intentional; do not report it.

## 3. How to build and test

```
go build ./...
gofmt -l .                       # must print nothing
go vet ./...
go test ./... -race -count=1     # all packages must pass
./build.sh                       # dist/mtrad
```

There is no network access needed for the tests. If something fails or does not compile, say so first, with the exact output.

## 4. Architecture you need to know

Directory map (non-test lines of Go, approximate):

| Path | Role |
|---|---|
| `cmd/mtrad/` (3.9k) | CLI entry, `runAll`, stage wiring, `uibackend.go` (real backend of the menu), `uicmd.go`, `logs.go` |
| `internal/schema/` | JSONL read/write with `_schema` header, `*.done.json` freshness hashes, work-dir lock, `ErrLog` (`errors.jsonl`), exit codes (0 ok, 1 error, 2 usage, **3 partial**) |
| `internal/jarx/`, `scan/`, `extract/`, `patchouli/`, `ftbq/`, `relaxed/`, `jsonx/` | Reading jars (zip), lang files, Patchouli, SNBT, lenient JSON |
| `internal/diff/` | Which texts need translation; reuse of existing pt_br; `ModStats` heuristic |
| `internal/protect/` | Replaces format codes and placeholders with `⟦n⟧` marks before sending to the model, restores them after |
| `internal/route/`, `translate/` (2.3k) | Choose engine per text; engines; cache; retries; request log; resume |
| `internal/validate/` | Decides what may enter the pack (`ok.jsonl`) and what is rejected (`failed.jsonl`); applies manual overrides |
| `internal/review/` | `review.jsonl`: manual fixes / "leave in English" decisions |
| `internal/pack/`, `install/`, `activate/` | Build the zip, copy FTB Quests files, switch pack and language on in `options.txt`; `pack/verify.go` (`mtrad verify`) re-opens a built zip and checks it against the stored originals (new, least-reviewed code) |
| `internal/doctor/`, `runlog/`, `glossary/`, `vanilla/`, `version/`, `evalset/`, `assets/` | Environment checks, per-run logs, glossary, vanilla strings, version detection, test set, embedded worker |
| `internal/ui/` (2.8k) | Full-screen terminal menu; talks only to the `Backend` interface (`backend.go`); `Demo` is a fake backend |
| `cmd/mtrad-prototype`, `cmd/mtrad-ui`, `cmd/prescript` | Legacy / experimental programs. Lowest priority |

Work-directory files (all under `<work>/`): `strings*.jsonl`, `todo.jsonl`, `protected.jsonl`, `routed.jsonl`, `translated.jsonl` (append-only), `ok.jsonl`, `failed.jsonl`, `review.jsonl`, `errors.jsonl`, `*.done.json`, `.lock`, `logs/`, `out/`. The translation cache is `cache/cache.jsonl`.

### Invariants that must hold

1. **Never destroy user data.** The work directory and the translation cache are never deleted by the program. Game files are never overwritten silently: `install --overwrite` keeps a `.bak`; `activate` backs `options.txt` up to `options.txt.mtrad.bak` and refuses while the game is running; `SaveSetup` keeps the previous `mtrad.json` as `.bak`.
2. **Atomic outputs.** The pack zip, FTB Quests files, `options.txt`, config and `done` files are written to a temp file and renamed. A crash must not leave a half-written file in a place the game or a later stage reads.
3. **Freshness.** A stage must refuse to run on inputs that changed after the previous stage produced them (`schema.RequireFresh` / `SkipFreshness`), except where explicitly allowed.
4. **Idempotent resume.** A translation is identified by text ID **and** hash of the (masked) source. Source changed → retranslated. Validation-rejected output → requested again on the next run, with temperature 0.5. Manually reviewed text → never sent to an engine again. Interrupting (Ctrl-C / Esc) keeps what was translated and builds a partial pack; running again continues.
5. **Format tokens survive.** `§x`/`&x` colour codes, `%s`, `%d`, `%1$s`, `%%`, `{0}`, `⟦n⟧` marks, newlines, MiniMessage-like tags etc. must be preserved. `validate` rejects outputs that lose, add or reorder them in a harmful way. A wrong text must **never** reach the pack through a path that skips `validate`.
6. **One writer per work directory.** `schema.Lock` writes the PID; a stale lock (dead PID) can be taken over; a live one is never stolen.
7. **Exit codes.** 3 means "finished with items that need attention" and is not an error for callers (the menu and `runAll` rely on this).
8. **The menu owns the terminal.** While a stage runs from the menu its stdout/stderr go to the run log (`runlog.StartQuiet`), never to the screen. The terminal mode is always restored on exit, panic, SIGTERM and SIGHUP.
9. **No secrets on disk.** API keys come from environment variables named in the config (`api_key_env`); the program never writes them anywhere.
10. **Untrusted input.** Jar files, their JSON/SNBT/TOML, the engine's responses, and file names inside archives are untrusted. The program never executes anything from a jar. The only thing it executes is the `worker` command the user wrote in their own config.

## 5. Where to look hardest (suggested order)

1. **Archive and path handling** (`jarx`, `scan`, `extract`, `pack`, `install`): zip-slip (`..`, absolute paths, backslashes, drive letters), symlinks, entries with huge declared sizes or compression ratios (zip bombs), nested jars (depth and total size limits), duplicate entry names, case collisions, non-UTF-8 names, `\r\n`, BOMs.
2. **Concurrency** (`translate` worker pool, request log, cache writer, `runlog` pipes, `ui` polling goroutines): data races that `-race` could miss, goroutine leaks on cancel, deadlocks on a full channel, double close, results written after the context is cancelled, a cancelled run that still reports success.
3. **Persistence and crash safety** (`schema`, `translate/cache`, `pack`, `install`, `activate`): partial writes, rename across file systems, missing `fsync` where it matters, append-only files that can get a torn last line and then fail to parse forever, lock files that survive a crash, `.bak` files that are overwritten by a second run and lose the original.
4. **Validation and protection logic** (`protect`, `validate`): token round-trip bugs (a placeholder that looks like a mark, nested or adjacent codes, `%` handling, `%%`, positional `%1$s`), Unicode (combining characters, NBSP, RTL), texts that are only a mark, empty output, output equal to input, "explanation" answers from the model (`Sure! Here is...`), quotes added around the answer, truncated answers (finish reason `length`).
5. **Engines** (`translate/engine_*.go`): HTTP timeouts, retry/backoff with a cancelled context, response parsing with missing fields, very large responses, status codes, `Retry-After`, the `worker` engine's process lifecycle (hung child, partial JSON line, stderr flooding, zombie processes after cancel).
6. **The menu** (`internal/ui`, `cmd/mtrad/uibackend.go`): frame never larger than the window at any size (including 1×1, 0×0 when `stty size` fails), wide runes and combining marks in width calculation, input handling (escape sequences split across reads, bracketed paste, UTF-8 split across reads), terminal restore, `Backend` calls that can block the draw loop, state that goes stale after a stage runs (`invalidate`), per-instance work directory selection (`workFor`) picking the wrong directory, configuration written by the menu losing fields the user added by hand.
7. **`diff` / `ModStats` heuristic** for "repetitive" mods: false positives that would hide a mod that should be translated.
8. **CLI argument parsing and defaults** for each stage: wrong defaults, flags that are accepted and ignored, relative vs absolute paths after `chdir`, `~` handling, Windows path separators.

## 6. What to report and what not to

**Report:** crashes and panics (nil dereference, index out of range, closed channel), data loss, security issues (path traversal, zip bombs, command injection, unsafe temp files, TOCTOU on lock or `options.txt`), races, deadlocks, wrong results (a corrupted or wrong text reaching the pack, a stage reporting success when it failed, a stage skipping work it should do), resource leaks, error values that are dropped, and tests that pass but do not test what their name says.

**Do not report:** formatting, naming, Portuguese vs English, "add more comments", missing features that are not described above, suggestions to add dependencies, generic advice ("add logging", "use a framework"), or anything you did not verify in the code. If you are not sure, say so and mark it `PLAUSIBLE`.

### Verification rules

- A finding is `CONFIRMED` only if you **ran something**: a failing test you wrote, a small Go program, or a command with its output. Include it.
- Otherwise it is `PLAUSIBLE`, and you must say exactly which line you read and which input would trigger it.
- Before reporting, check whether an existing test already covers the case and whether the "known limitations" below already describe it.
- Prefer **fewer, real** findings over many speculative ones. Fifteen well-argued findings are better than eighty guesses.
- Do not modify the source tree in your report. If you write a failing test, include it as text in the finding.

## 7. Known limitations (do not report these as new)

- The `stty`-based key reading does not work on Windows; Windows only builds.
- Texts hard-coded in mod code (not in lang files) cannot be translated by a resource pack.
- The "repetitive mod" detection in `diff.ModStats` is a heuristic and can be wrong; the user decides.
- The reason column in the failed list shows the internal `⟦n⟧` mark next to the original code.
- The edit box in the menu starts with the model's text and has no "clear line" shortcut.
- Better Questing and other quest mods are not supported; only FTB Quests.
- Real-GPU / real-Ollama behaviour is not covered by the tests (they use fake servers and an `echo` engine).
- The cause of some already-translated texts being requested again after re-reading mods in an older version is not proven (stage 1 re-translation); the hash-keyed resume described in invariant 4 is the current mitigation.
- Quality of the model's translations is out of scope; only the pipeline's correctness is.

### Already found and fixed in an earlier internal pass (check the fixes, do not re-report the original defects)

An earlier review pass (by another model, with the same brief) reported 13 findings. All were verified and handled. Please **review the fixes themselves** for new bugs:

- F1 `validate`: a translation made for an older English text is now pending (`Counts.Stale`), not approved or failed (`staleTranslation`, `internal/validate/validate.go`).
- F2 `mtrad activate`: used to panic with "flag redefined: force"; now `newFlagsNoForce` (`cmd/mtrad/main.go`).
- F3 `validate.Check` step 6: reordered plain `%s`/`%d` specifiers are converted to positional ones (`reorderedSpecs`).
- F4 path traversal: names from jars are checked with `schema.SafeEntryName` in `scan`, `patchouli` and again in `pack.Assemble` (`dropUnsafe`); `WriteFTB` compares `rel == ".."` or the `".."+separator` prefix.
- F5 `activate`: `options.txt.mtrad.bak` is never overwritten; later backups are `.bak.2`, `.bak.3` (`freeBackupName`).
- F6 `translate.Run`: texts with a still-valid manual review (EnSha matches `todo.jsonl`) are never sent to an engine.
- F7 `worker` engine: `readLine` and `start` take `ctx`; a watcher goroutine kills the child on cancel.
- F8 `scan`: duplicate nested jars are read once; a per-top-level-jar budget (`MaxNestedJars`, `MaxNestedBytes`) stops hostile jars.
- F9 `schema.Appender`: each record is written in one `Write`; a partial write is truncated back.
- F10 fsync before rename (`schema.WriteFileSync`, `Writer.Close`, `WriteZip`, `activate`, `install`).
- F11 `schema.Lock`: release removes only a lock that still holds our PID; takeover of a stale lock is a rename plus check (`takeOverStale`).
- F12 `validate`: `&x` colour codes in lang texts produce a warning when they change.
- F13 `translate` progress callback is serialized and monotonic.

The internal pass did **not** read these areas, so they are the least reviewed: `internal/ui`, `cmd/mtrad/uibackend.go`, `internal/diff` (`ModStats`), `internal/relaxed`, `translate/engine_libre.go`, `internal/doctor`, `internal/version`, `internal/glossary`, `internal/review`, and `internal/pack/verify.go` (new: `mtrad verify`).

## 8. Output format

Start with a 3-line summary: build/vet/test result, number of findings per severity, and the single most dangerous finding.

Then one block per finding, ordered from most to least severe:

```
### F<n>. <one-line title>
Severity: critical | high | medium | low
Status: CONFIRMED | PLAUSIBLE
Where: path/to/file.go:LINE (function name)
What is wrong: <one or two sentences>
How to trigger: <exact input, command or sequence of events; for CONFIRMED, the commands you ran and their output>
Effect: <what the user loses or sees>
Suggested fix: <the change, in words or as a small diff; keep it minimal and in the style of the surrounding code>
Test to add: <name and what it asserts>
```

Severity guide: **critical** = data loss, wrong text silently in the pack, code execution, or path traversal; **high** = crash, hang or lost progress in a normal workflow; **medium** = wrong behaviour in an edge case a real modpack can hit; **low** = cosmetic or very unlikely.

Finish with a short list of areas you read but found no problems in, so the next reviewer does not repeat them.
