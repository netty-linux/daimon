package main

import (
	"encoding/json"
	"errors"
	"io"
	"path"
	"regexp"
	"strings"

	"github.com/netty-linux/daimon/internal/agentloop"
)

// An intentionally closed template, not a natural-language parser. Unsupported
// wording is left untouched, even when the operator opts in.
var scopePattern = regexp.MustCompile(`^Analise esta fixture\. Proponha criar um arquivo curto de documentacao em ([A-Za-z0-9._/-]+/) e alterar somente ([A-Za-z0-9._/-]+) de ([A-Za-z0-9_=+-]{1,128}) para ([A-Za-z0-9_=+-]{1,128})\. Nao execute alteracoes\. Use ferramentas somente quando precisar de evidencia\. Registre incertezas em Bloqueios\.$`)
var scopePathPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

const scopeStart = "<daimon-plan-scope>"
const scopeEnd = "</daimon-plan-scope>"
const maxScopeBytes = 16 * 1024

const scopeProtocol = `
Contrato estruturado de escopo opt-in: mantenha as sete seções humanas em texto simples, com os títulos exatos em linhas próprias e sem cercas markdown. Ao final, inclua exatamente um bloco de no máximo 16384 bytes, delimitado por <daimon-plan-scope> e </daimon-plan-scope>, cada delimitador em sua própria linha; JSON entre essas linhas, sem markdown.
JSON obrigatório: {"create":["diretorio/arquivo"],"modify":["arquivo"],"delete":[],"operations":[{"path":"arquivo","old":"texto antigo","new":"texto novo"}],"blockers":[]}.
Liste todas as alterações propostas no bloco, fielmente às seções humanas e ao pedido original. Não inclua alterações extras. blockers aceita somente os códigos information_missing, read_denied, read_failed, budget_exhausted; ausência de escrita não é bloqueio. O bloco é proposta, nunca autorização ou execução.`

type planScope struct {
	directory, file, old, new string
	recovered                 bool
}

func recognizePlanScope(request string) *planScope {
	m := scopePattern.FindStringSubmatch(request)
	if m == nil {
		return nil
	}
	dir := strings.TrimSuffix(m[1], "/")
	if !scopePath(dir) || !scopePath(m[2]) || m[3] == m[4] {
		return nil
	}
	return &planScope{directory: dir, file: m[2], old: m[3], new: m[4]}
}

func scopePath(p string) bool {
	if len(p) > 4096 || !scopePathPattern.MatchString(p) || p == "." || strings.HasPrefix(p, "/") || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasSuffix(part, ".") {
			return false
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		switch base {
		case "CON", "PRN", "AUX", "NUL":
			return false
		}
		if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
			return false
		}
	}
	return true
}

type proposedScope struct {
	Create     []string `json:"create"`
	Modify     []string `json:"modify"`
	Delete     []string `json:"delete"`
	Operations []struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
	} `json:"operations"`
	Blockers []string `json:"blockers"`
}

var errPlanScope = errors.New("plano incompatível com o contrato de escopo")

type planScopeError struct{}

func (planScopeError) Error() string { return errPlanScope.Error() }
func (planScopeError) Is(target error) bool {
	return target == errPlanScope || target == agentloop.ErrInvalidResponse
}

func splitScopeAnswer(answer string) (human, block string, ok bool) {
	if strings.Count(answer, scopeStart) != 1 || strings.Count(answer, scopeEnd) != 1 {
		return "", "", false
	}
	start := strings.Index(answer, scopeStart)
	end := strings.Index(answer, scopeEnd)
	if start == 0 || end < start+len(scopeStart)+2 || answer[start-1] != '\n' ||
		answer[start+len(scopeStart)] != '\n' || answer[end-1] != '\n' ||
		strings.TrimSpace(answer[end+len(scopeEnd):]) != "" {
		return "", "", false
	}
	human = strings.TrimSpace(answer[:start])
	block = answer[start+len(scopeStart)+1 : end-1]
	if len(block) > maxScopeBytes || strings.Contains(human, "```") || strings.Contains(human, "~~~") {
		return "", "", false
	}
	return human, block, true
}

func (s *planScope) valid(answer string) bool {
	human, block, ok := splitScopeAnswer(answer)
	if !ok {
		return false
	}
	// Require full heading lines in order, not inferred semantic equivalents.
	headings := []string{"Diagnóstico", "Objetivo da mudança", "Arquivos prováveis", "Alteração proposta por arquivo", "Riscos e suposições", "Validação proposta", "Bloqueios ou informações faltantes"}
	next := 0
	for _, line := range strings.Split(human, "\n") {
		line = strings.TrimSpace(line)
		for i, heading := range headings {
			if line == heading {
				if i != next {
					return false
				}
				next++
			}
		}
	}
	if next != len(headings) {
		return false
	}
	var p proposedScope
	if !uniqueScopeKeys(block) {
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(block))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil || decoder.Decode(new(any)) != io.EOF ||
		p.Create == nil || p.Modify == nil || p.Delete == nil || p.Operations == nil || p.Blockers == nil {
		return false
	}
	if len(p.Create) != 1 || !scopePath(p.Create[0]) || path.Dir(p.Create[0]) != s.directory ||
		p.Create[0] == s.file || len(p.Modify) != 1 || p.Modify[0] != s.file || len(p.Delete) != 0 || len(p.Operations) != 1 {
		return false
	}
	op := p.Operations[0]
	if op.Path != s.file || op.Old != s.old || op.New != s.new {
		return false
	}
	if len(p.Blockers) > 4 {
		return false
	}
	seenBlockers := map[string]bool{}
	for _, b := range p.Blockers {
		if seenBlockers[b] {
			return false
		}
		seenBlockers[b] = true
		switch b {
		case "information_missing", "read_denied", "read_failed", "budget_exhausted":
		default:
			return false
		}
	}
	return true
}

// Reject duplicate keys instead of accepting JSON's ambiguous last-value wins.
// This closed schema has only an object containing arrays and operation objects.
func uniqueScopeKeys(block string) bool {
	d := json.NewDecoder(strings.NewReader(block))
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 4 {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		open, composite := token.(json.Delim)
		if !composite {
			return true
		}
		if open != '{' && open != '[' {
			return false
		}
		seen := map[string]bool{}
		for d.More() {
			if open == '{' {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] {
					return false
				}
				if depth == 0 {
					switch name {
					case "create", "modify", "delete", "operations", "blockers":
					default:
						return false
					}
				} else if depth == 2 {
					switch name {
					case "path", "old", "new":
					default:
						return false
					}
				} else {
					return false
				}
				seen[name] = true
			}
			if !value(depth + 1) {
				return false
			}
		}
		close, err := d.Token()
		return err == nil && ((open == '{' && close == json.Delim('}')) || (open == '[' && close == json.Delim(']')))
	}
	if !value(0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}

func (s *planScope) validate(answer string) (string, error) {
	if s.valid(answer) {
		return "", nil
	}
	if s.recovered {
		return "", planScopeError{}
	}
	s.recovered = true
	return "Recuperação única de escopo: a resposta anterior não satisfaz o contrato estruturado. Releia o pedido original integral e as evidências no histórico. Corrija o plano e o bloco obrigatório, preservando as sete seções. Não solicite ferramentas, execução ou escrita; não invente evidências. Crie proposta de um documento no diretório solicitado, modifique exclusivamente o arquivo indicado pela troca literal solicitada e não proponha exclusões." + scopeProtocol, nil
}
