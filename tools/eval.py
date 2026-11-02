#!/usr/bin/env python3
"""eval: mede a qualidade de um motor com pares que já existem (módulo eval).

Fluxo:
  mtrad evalset -w work -o evalwork -n 500     # cria o conjunto de teste
  mtrad protect -w evalwork ; mtrad route -c cfg.json -w evalwork
  mtrad translate -c cfg.json -w evalwork ; mtrad validate -w evalwork
  python3 tools/eval.py evalwork               # compara com a tradução humana

Usa o sacreBLEU (BLEU e chrF) quando estiver instalado; sem ele calcula só um
chrF simplificado, que NÃO é idêntico ao do sacreBLEU.
"""
import json
import os
import sys
from collections import Counter


def read_jsonl(path):
    out = []
    if not os.path.exists(path):
        return out
    with open(path, encoding="utf-8") as f:
        first = f.readline()
        if '"_schema"' not in first:
            raise SystemExit("%s: sem cabeçalho de esquema" % path)
        for line in f:
            if line.strip():
                out.append(json.loads(line))
    return out


def ngrams(s, n):
    s = "".join(s.split())
    return Counter(s[i : i + n] for i in range(len(s) - n + 1))


def chrf_simple(hyp, ref, max_n=6, beta=2.0):
    """chrF simplificado: média de precisão e revocação dos n-gramas de
    caracteres (1 a 6, sem espaços) e F-beta dessas médias."""
    ps, rs = [], []
    for n in range(1, max_n + 1):
        h, r = ngrams(hyp, n), ngrams(ref, n)
        if not h or not r:
            continue
        match = sum((h & r).values())
        ps.append(match / sum(h.values()))
        rs.append(match / sum(r.values()))
    if not ps:
        return 0.0
    p, r = sum(ps) / len(ps), sum(rs) / len(rs)
    if p + r == 0:
        return 0.0
    b2 = beta * beta
    return 100.0 * (1 + b2) * p * r / (b2 * p + r)


def main(argv):
    if len(argv) != 2:
        raise SystemExit("uso: eval.py PASTA_DO_EVALSET")
    d = argv[1]
    ref = {r["id"]: r for r in read_jsonl(os.path.join(d, "reference.jsonl"))}
    ok = {r["id"]: r for r in read_jsonl(os.path.join(d, "ok.jsonl"))}
    failed = {r["id"]: r for r in read_jsonl(os.path.join(d, "failed.jsonl"))}
    if not ref:
        raise SystemExit("reference.jsonl vazio ou ausente (rode mtrad evalset)")
    ids = [i for i in ref if i in ok]
    hyps = [ok[i]["pt"] for i in ids]
    refs = [ref[i]["pt"] for i in ids]
    lines = ["# Avaliação de motor", ""]
    lines.append("- Textos de referência: %d" % len(ref))
    lines.append("- Aprovados na validação: %d" % len(ok))
    lines.append("- Reprovados: %d" % len(failed))
    lines.append("- Sem resposta: %d" % (len(ref) - len(ok) - len(failed)))
    if ref:
        lines.append("- Taxa de reprovação: %.1f%%" % (100.0 * len(failed) / len(ref)))
    scores = []
    try:
        import sacrebleu  # type: ignore

        if ids:
            bleu = sacrebleu.corpus_bleu(hyps, [refs])
            chrf = sacrebleu.corpus_chrf(hyps, [refs])
            scores.append("BLEU (sacreBLEU): %.1f" % bleu.score)
            scores.append("chrF (sacreBLEU): %.1f" % chrf.score)
    except ImportError:
        if ids:
            avg = sum(chrf_simple(h, r) for h, r in zip(hyps, refs)) / len(ids)
            scores.append("chrF simplificado (sacreBLEU não instalado; não é igual ao oficial): %.1f" % avg)
    lines.append("")
    lines.extend("- " + s for s in scores)
    lines.append("")
    lines.append("Os escores valem só para os %d textos aprovados; leia também as taxas acima." % len(ids))
    worst = sorted(ids, key=lambda i: chrf_simple(ok[i]["pt"], ref[i]["pt"]))[:10]
    if worst:
        lines += ["", "## Dez piores (chrF simplificado)", "", "| Original | Motor | Humano |", "| --- | --- | --- |"]
        for i in worst:
            esc = lambda s: s.replace("|", "\\|").replace("\n", "\\n")  # noqa: E731
            lines.append("| %s | %s | %s |" % (esc(ref[i]["en"]), esc(ok[i]["pt"]), esc(ref[i]["pt"])))
    text = "\n".join(lines) + "\n"
    with open(os.path.join(d, "eval.md"), "w", encoding="utf-8") as f:
        f.write(text)
    sys.stdout.write(text)


if __name__ == "__main__":
    main(sys.argv)
