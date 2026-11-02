package ui

import "context"

// Instance é uma instância do Prism Launcher.
type Instance struct {
	Name, Path, MC, Loader, LoaderVer, PackFormat string
}

// Mod é um mod com a quantidade de textos que ele tem.
type Mod struct {
	ID         string
	Texts      int
	Repetitive bool // nomes do tipo "<material> <forma>": quase sempre não vale traduzir
}

// Failed é um texto que não passou na validação.
type Failed struct {
	ID                        string // identificador interno (o que as correções usam)
	Name                      string // como mostrar (mod:chave); vazio = o ID
	Source, Reason, Orig, Out string
	Edit                      string // correção feita à mão (vazio = nenhuma)
	Left                      bool   // o usuário decidiu deixar em inglês
}

// Title é o nome mostrado na lista.
func (f Failed) Title() string {
	if f.Name != "" {
		return f.Name
	}
	return f.ID
}

// Check é um item do "verificar o ambiente".
type Check struct {
	Name   string
	OK     bool
	Warn   bool // atenção, mas não impede (só vale com OK falso)
	Detail string
}

// Status é o que o painel do topo mostra.
type Status struct {
	OllamaUp bool
	Model    string
	OnGPU    bool
	// Idle: o Ollama está no ar, mas o modelo ainda não foi carregado (não dá
	// para dizer se vai usar a GPU).
	Idle           bool
	OK, Fail, Miss int
	LastRun        string
	// Known é falso enquanto não houve nenhuma execução nesta instância (os
	// números acima não valem ainda).
	Known bool
	// Work é a pasta de trabalho desta instância e Pack o .zip que o pack monta
	// (vazios = a interface usa o que o usuário escolheu no setup).
	Work, Pack string
}

// Setup é o que o passo a passo grava no mtrad.json.
type Setup struct {
	Engine, Model string   // ollama, llama, gemini, libre ou ct2; nome do modelo
	Sources       []string // lang, patchouli, ftbq
	Work, Config  string   // pasta de trabalho e arquivo de configuração
}

// Estimate é a estimativa que a tela de traduzir mostra antes de começar.
type Estimate struct {
	Texts  int // 0 = ainda não se sabe (nenhuma leitura dos mods foi feita)
	PerMin int // 0 = sem medida de execuções anteriores
}

// Outcome é o resumo do fim de uma tradução ou de uma aplicação de correções.
type Outcome struct {
	Lines   []string // o que o mtrad imprimiria (traduzidos, cache, falhas...)
	Partial bool     // terminou com itens falhos ou incompleto (código de saída 3)
	// Problems são os erros mais comuns da execução (do errors.jsonl), já
	// resumidos ("3 × translate: …"), para quem lê "falhou" saber o motivo sem
	// abrir arquivos.
	Problems []string
}

// Backend é tudo o que a interface precisa do resto do programa. Na demonstração
// ele é falso (demo.go); na versão de verdade (cmd/mtrad/uibackend.go), cada
// método chama a etapa do mtrad.
type Backend interface {
	// IsDemo diz se nada é executado de verdade.
	IsDemo() bool
	// Defaults é a configuração que já existe (lida do mtrad.json) ou o padrão
	// da primeira vez; o setup parte dela.
	Defaults() Setup
	// SetupDone diz se a configuração inicial já foi feita (na versão de verdade:
	// existe um mtrad.json válido).
	SetupDone() bool
	// SaveSetup grava o mtrad.json; nunca apaga o que o usuário já tinha nele
	// (guarda o anterior em .bak).
	SaveSetup(s Setup) error
	Instances() []Instance
	// SelectInstance lê a versão, o loader e o formato do pacote da instância
	// escolhida e passa a trabalhar com ela. Devolve a instância completa.
	SelectInstance(in Instance) (Instance, error)
	Status() Status
	Models() []string
	Doctor() []Check
	// Prepare lê os mods da instância (scan, extract, diff), sem traduzir, para
	// a lista de mods e a estimativa terem números.
	Prepare(ctx context.Context) error
	Mods() []Mod
	// Skipped são os mods que hoje ficam em inglês (skip_mods).
	Skipped() []string
	SaveSkip(ids []string) error
	Estimate(limit int) Estimate
	// Translate roda todas as etapas; limit > 0 é o teste pequeno; install copia
	// os arquivos do FTB Quests no fim. Cancelar o ctx pausa: o que já foi
	// traduzido fica guardado e o pacote parcial é montado.
	Translate(ctx context.Context, limit int, install bool, progress func(done, total int)) (Outcome, error)
	Failed() []Failed
	SaveEdit(id, text string) error
	Leave(id string) error
	// Apply valida e monta o pacote de novo, sem traduzir (aplica as correções).
	Apply(ctx context.Context) (Outcome, error)
	// Install devolve o que seria copiado (dry) ou o que foi copiado.
	Install(dry bool) ([]string, error)
	// Verify confere o pacote montado contra os originais (Outcome.Partial =
	// achou problemas; Outcome.Lines = o relatório).
	Verify() (Outcome, error)
	// Activate liga o pacote e o idioma no options.txt (o jogo precisa estar fechado).
	Activate(dry bool) ([]string, error)
	Logs(n int) []string
	Unlock() string
}
