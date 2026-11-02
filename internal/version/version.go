// Package version descobre a versão do Minecraft, o loader e o formato de
// resource pack de uma instância do Prism Launcher.
package version

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"mtrad/internal/schema"
)

type Options struct {
	InstanceDir string
	// Valores que ganham do que foi descoberto (opcionais).
	McVersion     string
	Loader        string
	LoaderVersion string
	PackMajor     int
	PackMinor     int
}

var loaderUIDs = map[string]string{
	"net.fabricmc.fabric-loader": "fabric",
	"org.quiltmc.quilt-loader":   "quilt",
	"net.neoforged":              "neoforge",
	"net.minecraftforge":         "forge",
}

type mmcPack struct {
	Components []struct {
		UID     string `json:"uid"`
		Version string `json:"version"`
	} `json:"components"`
}

// PackFormatFor devolve o formato de resource pack de uma versão lançada.
func PackFormatFor(mc string) (schema.PackFormat, bool) {
	for _, r := range releases {
		if r.ID == mc {
			return schema.PackFormat{Major: r.Major, Minor: r.Minor}, true
		}
	}
	return schema.PackFormat{}, false
}

func Detect(o Options) (schema.Instance, error) {
	if abs, err := filepath.Abs(o.InstanceDir); err == nil {
		o.InstanceDir = abs // as etapas seguintes podem rodar em outra pasta
	}
	in := schema.Instance{Schema: schema.VInstance, InstanceDir: o.InstanceDir, How: "mmc-pack"}
	st, err := os.Stat(o.InstanceDir)
	if err != nil || !st.IsDir() {
		return in, fmt.Errorf("%s não é uma pasta", o.InstanceDir)
	}
	// pasta do jogo: o Prism usa uma subpasta da instância
	for _, n := range []string{"minecraft", ".minecraft"} {
		p := filepath.Join(o.InstanceDir, n)
		if s, err := os.Stat(p); err == nil && s.IsDir() {
			in.GameDir = p
			break
		}
	}
	if in.GameDir == "" {
		return in, fmt.Errorf("não achei minecraft/ nem .minecraft/ em %s", o.InstanceDir)
	}
	// versão e loader
	mmcPath := filepath.Join(o.InstanceDir, "mmc-pack.json")
	if b, err := os.ReadFile(mmcPath); err == nil {
		var mp mmcPack
		if err := json.Unmarshal(b, &mp); err != nil {
			in.Warnings = append(in.Warnings, "mmc-pack.json ilegível: "+err.Error())
		}
		for _, c := range mp.Components {
			if c.UID == "net.minecraft" {
				in.McVersion = c.Version
			}
			if l, ok := loaderUIDs[c.UID]; ok && in.Loader == "" {
				in.Loader, in.LoaderVersion = l, c.Version
			}
		}
	} else {
		in.How = "override"
		in.Warnings = append(in.Warnings, "sem mmc-pack.json; usando só o que foi informado")
	}
	if o.McVersion != "" {
		in.McVersion = o.McVersion
		in.How += "+override"
	}
	if o.Loader != "" {
		in.Loader = o.Loader
	}
	if o.LoaderVersion != "" {
		in.LoaderVersion = o.LoaderVersion
	}
	if in.McVersion == "" {
		return in, fmt.Errorf("não consegui descobrir a versão do Minecraft; informe com --mc")
	}
	if in.Loader == "" {
		in.Warnings = append(in.Warnings, "loader não identificado")
	} else if in.Loader == "forge" || in.Loader == "neoforge" {
		note := "testado em 1 modpack real (Reclamation, Forge 1.20.1)"
		if in.Loader == "neoforge" {
			note = "ainda sem teste num modpack real de NeoForge (o de Forge 1.20.1 foi testado)"
		}
		in.Warnings = append(in.Warnings, "loader "+in.Loader+": "+note+"; lê o nome do mod por mods.toml, os mods embutidos em META-INF/jarjar, lang, Patchouli e FTB Quests")
	} else if in.Loader != "fabric" && in.Loader != "quilt" {
		in.Warnings = append(in.Warnings, "loader "+in.Loader+": o script foi pensado para Fabric")
	}
	// formato do pacote
	switch {
	case o.PackMajor > 0:
		in.PackFormat = schema.PackFormat{Major: o.PackMajor, Minor: o.PackMinor}
		in.How += "+pack-override"
	default:
		if pf, ok := PackFormatFor(in.McVersion); ok {
			in.PackFormat = pf
		} else if pf, ok := fromClientJar(o.InstanceDir, in.McVersion); ok {
			in.PackFormat = pf
			in.How += "+client-jar"
		} else {
			return in, fmt.Errorf("não conheço o formato de pacote da versão %q; informe com --pack-format", in.McVersion)
		}
	}
	if v, err := strconv.Atoi(strings.SplitN(in.McVersion, ".", 3)[0]); err == nil && v == 1 {
		parts := strings.Split(in.McVersion, ".")
		if len(parts) > 1 {
			if m, err := strconv.Atoi(parts[1]); err == nil && m < 14 {
				if in.Loader == "fabric" || in.Loader == "quilt" {
					in.Warnings = append(in.Warnings, "o Fabric oficial só existe a partir da 1.14")
				} else {
					in.Warnings = append(in.Warnings, "Minecraft anterior à 1.13: os idiomas são arquivos .lang (lidos e gravados como tal); só o texto dos arquivos de idioma é traduzido (sem Patchouli em JSON, sem FTB Quests moderno)")
				}
			}
		}
	}
	return in, nil
}

// fromClientJar lê version.json do jar do cliente que o Prism guarda em
// <dados>/libraries/com/mojang/minecraft/<versão>/minecraft-<versão>-client.jar.
// O formato do campo pack_version varia entre versões; aceita número ou objeto.
func fromClientJar(instanceDir, mc string) (schema.PackFormat, bool) {
	dataDir := filepath.Dir(filepath.Dir(instanceDir)) // .../PrismLauncher/instances/<nome>
	jar := filepath.Join(dataDir, "libraries", "com", "mojang", "minecraft", mc, "minecraft-"+mc+"-client.jar")
	zr, err := zip.OpenReader(jar)
	if err != nil {
		return schema.PackFormat{}, false
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != "version.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return schema.PackFormat{}, false
		}
		b, _ := io.ReadAll(io.LimitReader(rc, 1<<20))
		rc.Close()
		return parsePackVersion(b)
	}
	return schema.PackFormat{}, false
}

func parsePackVersion(b []byte) (schema.PackFormat, bool) {
	var v struct {
		PackVersion json.RawMessage `json:"pack_version"`
	}
	if json.Unmarshal(b, &v) != nil || len(v.PackVersion) == 0 {
		return schema.PackFormat{}, false
	}
	var n int
	if json.Unmarshal(v.PackVersion, &n) == nil {
		return schema.PackFormat{Major: n}, true
	}
	var o struct {
		Resource      *int `json:"resource"`
		ResourceMajor *int `json:"resource_major"`
		ResourceMinor *int `json:"resource_minor"`
	}
	if json.Unmarshal(v.PackVersion, &o) != nil {
		return schema.PackFormat{}, false
	}
	switch {
	case o.ResourceMajor != nil:
		pf := schema.PackFormat{Major: *o.ResourceMajor}
		if o.ResourceMinor != nil {
			pf.Minor = *o.ResourceMinor
		}
		return pf, true
	case o.Resource != nil:
		return schema.PackFormat{Major: *o.Resource}, true
	}
	return schema.PackFormat{}, false
}

// Run grava instance.json em workDir.
func Run(workDir string, o Options) (schema.Instance, error) {
	in, err := Detect(o)
	if err != nil {
		return in, err
	}
	if err := schema.WriteJSON(filepath.Join(workDir, "instance.json"), in); err != nil {
		return in, err
	}
	if err := schema.WriteDone(workDir, "version", nil, []string{"instance.json"}, map[string]int{"warnings": len(in.Warnings)}); err != nil {
		return in, err
	}
	return in, nil
}
