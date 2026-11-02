#!/usr/bin/env python3
"""ct2worker: tradutor neural em CPU para o mtrad (processo filho do Go).

Protocolo (linhas JSON em stdin/stdout, ver seção 20 do documento):
  1. cada lado manda primeiro {"hello": 1}
  2. o Go manda {"id","text","src","tgt"}; este programa responde, na mesma
     ordem, {"id","out"} ou {"id","error"}.

STATUS: o protocolo foi testado com --backend echo. O backend "ct2" (CTranslate2
com modelos Marian/OPUS-MT) foi escrito a partir da documentação e NÃO foi
testado com o CTranslate2 real, porque a sessão em que foi escrito não
conseguia instalar pacotes do PyPI.

Exemplo de uso com um modelo OPUS-MT convertido:
  ct2-transformers-converter --model Helsinki-NLP/opus-mt-tc-big-en-pt \\
      --output_dir opus-en-pt --quantization int8
  python3 ct2worker.py --backend ct2 --model opus-en-pt \\
      --tokenizer Helsinki-NLP/opus-mt-tc-big-en-pt --text-prefix '>>pob<< '
"""
import argparse
import json
import sys


def send(obj):
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    sys.stdout.flush()


class EchoBackend:
    """Só para testar o protocolo: devolve o texto em maiúsculas."""

    def translate(self, texts):
        return [t.upper() for t in texts]


class CT2Backend:
    def __init__(self, a):
        import ctranslate2  # noqa: WPS433 (importação tardia de propósito)
        import transformers

        self.tok = transformers.AutoTokenizer.from_pretrained(a.tokenizer)
        self.tr = ctranslate2.Translator(
            a.model,
            device=a.device,
            inter_threads=a.inter_threads,
            intra_threads=a.threads,
            compute_type=a.compute_type,
        )
        self.beam = a.beam
        self.prefix = a.text_prefix

    def translate(self, texts):
        src = [self.tok.convert_ids_to_tokens(self.tok.encode(self.prefix + t)) for t in texts]
        res = self.tr.translate_batch(src, beam_size=self.beam, max_decoding_length=512)
        out = []
        for r in res:
            ids = self.tok.convert_tokens_to_ids(r.hypotheses[0])
            out.append(self.tok.decode(ids, skip_special_tokens=True))
        return out


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--backend", choices=["echo", "ct2"], default="ct2")
    p.add_argument("--model", help="pasta do modelo convertido para CTranslate2")
    p.add_argument("--tokenizer", help="nome ou pasta do tokenizador (transformers)")
    p.add_argument("--text-prefix", default="", help="texto colocado antes de cada frase, por exemplo '>>pob<< '")
    p.add_argument("--device", default="cpu")
    p.add_argument("--threads", type=int, default=4)
    p.add_argument("--inter-threads", type=int, default=1)
    p.add_argument("--compute-type", default="int8")
    p.add_argument("--beam", type=int, default=4)
    a = p.parse_args()

    first = sys.stdin.readline()
    try:
        ok = json.loads(first).get("hello") == 1
    except Exception:
        ok = False
    if not ok:
        sys.stderr.write("ct2worker: esperava {\"hello\":1}\n")
        sys.exit(2)
    try:
        backend = EchoBackend() if a.backend == "echo" else CT2Backend(a)
    except Exception as e:  # falha ao carregar o modelo
        sys.stderr.write("ct2worker: não consegui carregar o modelo: %r\n" % (e,))
        sys.exit(3)
    send({"hello": 1})

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        req = json.loads(line)
        try:
            out = backend.translate([req["text"]])[0]
            send({"id": req["id"], "out": out})
        except Exception as e:
            send({"id": req["id"], "error": repr(e)})


if __name__ == "__main__":
    main()
