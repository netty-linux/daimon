package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/managedworkspace"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

var errManagedArguments = errors.New("Uso: managed-workspace --base <store-privado> create --source <origem> | apply --run <id> --plan <arquivo> [--enable-replace-file --enable-preimage-retention] | report --run <id> | list | inspect --run <id> | discard --run <id> --enable-discard | export-evidence --run <id> --destination <novo-diretório-absoluto> --source-check <origem-absoluta> --enable-export-evidence. export-output --run <id> --path <output-relativo> --destination <nome-lógico> --source-check <origem-absoluta> --enable-output-export. export-preimage --run <id> --operation <operation-id> --destination <nome-lógico> --source-check <origem-absoluta> --enable-preimage-export. Patch export está fora do escopo")

func managedArguments(args []string) (string, string, map[string]string, error) {
	if len(args) < 3 || args[0] != "--base" || args[1] == "" {
		return "", "", nil, errManagedArguments
	}
	command := args[2]
	allowed := map[string]bool{}
	switch command {
	case "create":
		allowed["--source"] = true
	case "apply":
		allowed["--run"] = true
		allowed["--plan"] = true
		allowed["--enable-replace-file"] = true
		allowed["--enable-preimage-retention"] = true
	case "report", "inspect":
		allowed["--run"] = true
	case "list":
	case "discard":
		allowed["--run"] = true
		allowed["--enable-discard"] = true
	case "export-evidence":
		allowed["--run"] = true
		allowed["--destination"] = true
		allowed["--source-check"] = true
		allowed["--enable-export-evidence"] = true
	case "export-preimage":
		for _, flag := range []string{"--run", "--operation", "--destination", "--source-check", "--enable-preimage-export"} {
			allowed[flag] = true
		}
	case "export-output":
		for _, flag := range []string{"--run", "--path", "--destination", "--source-check", "--enable-output-export"} {
			allowed[flag] = true
		}
	default:
		return "", "", nil, errManagedArguments
	}
	values := map[string]string{}
	for i := 3; i < len(args); {
		if (command == "apply" && (args[i] == "--enable-replace-file" || args[i] == "--enable-preimage-retention")) || (args[i] == "--enable-discard" && command == "discard") || (args[i] == "--enable-export-evidence" && command == "export-evidence") || (args[i] == "--enable-output-export" && command == "export-output") || (args[i] == "--enable-preimage-export" && command == "export-preimage") {
			if values[args[i]] != "" {
				return "", "", nil, errManagedArguments
			}
			values[args[i]] = "true"
			i++
			continue
		}
		if i+1 >= len(args) || !allowed[args[i]] || values[args[i]] != "" || args[i+1] == "" {
			return "", "", nil, errManagedArguments
		}
		values[args[i]] = args[i+1]
		i += 2
	}
	required := len(allowed)
	if command == "apply" {
		required = 2
		if values["--run"] == "" || values["--plan"] == "" || (values["--enable-preimage-retention"] != "" && values["--enable-replace-file"] == "") {
			return "", "", nil, errManagedArguments
		}
		delete(allowed, "--enable-replace-file")
		delete(allowed, "--enable-preimage-retention")
		for k := range values {
			if k == "--enable-replace-file" || k == "--enable-preimage-retention" {
				required++
			}
		}
	}
	if len(values) != required {
		return "", "", nil, errManagedArguments
	}
	return args[1], command, values, nil
}
func runManagedWorkspace(ctx context.Context, args []string, input io.Reader, out, display io.Writer) error {
	base, command, values, err := managedArguments(args)
	if err != nil {
		return err
	}
	if !managedworkspace.Supported() {
		return managedworkspace.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if command == "list" || command == "inspect" || command == "discard" || command == "export-evidence" || command == "export-output" || command == "export-preimage" {
		store, err := managedworkspace.OpenStore(base)
		if err != nil {
			return err
		}
		storeClosed := false
		defer func() {
			if !storeClosed {
				store.Close()
			}
		}()
		if command == "export-preimage" {
			proposal, err := store.PreparePreimage(ctx, values["--run"], values["--operation"], values["--destination"], values["--source-check"], values["--enable-preimage-export"] == "true")
			if err != nil {
				return err
			}
			defer proposal.Close()
			permit, err := proposal.Approve(ctx, &preimageReviewer{input: input, display: display})
			if err != nil {
				return err
			}
			result, operationErr := permit.Export(ctx)
			storeClosed = true
			if store.Close() != nil {
				result.State = "unknown_interrupted"
				operationErr = errors.Join(operationErr, managedworkspace.ErrArtifact)
			}
			if e := ctx.Err(); e != nil {
				result.State = "unknown_interrupted"
				operationErr = errors.Join(operationErr, e)
			}
			if e := writeManagedResult(out, fmt.Sprintf("Estado do Preimage Export: %s\nArquivos de conteúdo: %d; bytes: %d\nConteúdo potencialmente sensível; origem não modificada.\n", result.State, result.Files, result.Bytes)); e != nil {
				operationErr = errors.Join(operationErr, e)
			}
			return operationErr
		}
		if command == "export-output" {
			proposal, err := store.PrepareOutput(ctx, values["--run"], values["--path"], values["--destination"], values["--source-check"], values["--enable-output-export"] == "true")
			if err != nil {
				return err
			}
			defer proposal.Close()
			permit, err := proposal.Approve(ctx, &outputReviewer{input: input, display: display})
			if err != nil {
				return err
			}
			result, operationErr := permit.Export(ctx)
			storeClosed = true
			if store.Close() != nil {
				result.State = "unknown_interrupted"
				operationErr = errors.Join(operationErr, managedworkspace.ErrArtifact)
			}
			if e := ctx.Err(); e != nil {
				result.State = "unknown_interrupted"
				operationErr = errors.Join(operationErr, e)
			}
			if e := writeManagedResult(out, fmt.Sprintf("Estado do Output Export: %s\nArquivos de conteúdo: %d; bytes: %d\nConteúdo potencialmente sensível; origem não modificada.\n", result.State, result.Files, result.Bytes)); e != nil {
				operationErr = errors.Join(operationErr, e)
			}
			return operationErr
		}
		if command == "export-evidence" {
			proposal, err := store.PrepareEvidence(ctx, values["--run"], values["--destination"], values["--source-check"], values["--enable-export-evidence"] == "true")
			if err != nil {
				return err
			}
			defer proposal.Close()
			permit, err := proposal.Approve(ctx, editcontract.NewTerminal(input, display))
			if errors.Is(err, editcontract.ErrDenied) {
				return &outputError{message: "Exportação de evidências negada; publicação não iniciada", cause: err}
			}
			if errors.Is(err, editcontract.ErrApproval) {
				return &outputError{message: "Falha ao exibir ou obter aprovação de exportação; publicação não iniciada", cause: err}
			}
			if err != nil {
				return err
			}
			result, err := permit.Export(ctx)
			storeClosed = true
			return finishEvidenceExport(ctx, result, err, store.Close, out)
		}
		if command == "list" {
			summaries, err := store.List(ctx)
			if err != nil {
				return err
			}
			for i := range summaries {
				summaries[i].Retention = nil
			}
			return printManagedSummaries(out, summaries)
		}
		if command == "inspect" {
			summary, err := store.Inspect(ctx, values["--run"])
			if err != nil {
				return err
			}
			return printManagedSummaries(out, []managedworkspace.Summary{summary})
		}
		proposal, err := store.PrepareDiscard(ctx, values["--run"], values["--enable-discard"] == "true")
		if err != nil {
			return err
		}
		defer proposal.Close()
		permit, err := proposal.Approve(ctx, editcontract.NewTerminal(input, display))
		if err != nil {
			if errors.Is(err, editcontract.ErrDenied) {
				return errors.New("Descarte negado; remoção não iniciada")
			}
			if errors.Is(err, editcontract.ErrApproval) {
				return errors.New("Falha ao exibir ou obter aprovação; remoção não iniciada")
			}
			return err
		}
		state, err := permit.Discard(ctx)
		if outputErr := writeManagedResult(out, fmt.Sprintf("Estado do descarte: %s\nOrigem: não modificada pelo descarte\n", state)); outputErr != nil {
			return outputErr
		}
		return err
	}
	if command == "create" {
		r, err := managedworkspace.Create(ctx, base, values["--source"])
		if err != nil {
			return err
		}
		defer r.Close()
		m := r.Manifest()
		return writeManagedResult(out, fmt.Sprintf("Cópia privada criada. Origem não publicada.\nRun ID: %s\nArquivos: %d; diretórios: %d; bytes: %d\n", m.RunID, m.Files, m.Directories, m.Bytes))
	}
	r, err := managedworkspace.Open(base, values["--run"])
	if err != nil {
		return err
	}
	defer r.Close()
	if command == "report" {
		report, err := r.Report()
		if err != nil {
			return err
		}
		return printManagedReport(out, report)
	}
	plan, err := managedworkspace.ReadPlan(ctx, values["--plan"])
	if err != nil {
		return managedPlanError(err)
	}
	p, err := r.PrepareWithOptions(ctx, plan, managedworkspace.ApplyOptions{RetainPreimages: values["--enable-preimage-retention"] == "true", ReplaceEnabled: values["--enable-replace-file"] == "true"})
	if err != nil {
		return managedPlanError(err)
	}
	permit, err := p.Approve(ctx, editcontract.NewTerminal(input, display))
	if errors.Is(err, editcontract.ErrDenied) {
		if report, e := r.Report(); e == nil {
			if e = printManagedReport(out, report); e != nil {
				return e
			}
		}
		return errors.New("Aprovação negada; cópia de trabalho não aplicada")
	}
	if err != nil {
		if errors.Is(err, editcontract.ErrApproval) {
			return errors.New("Falha ao exibir ou obter aprovação; aplicação não iniciada")
		}
		return err
	}
	report, err := permit.Apply(ctx)
	if report.Version != 0 {
		if displayErr := printManagedReport(out, report); displayErr != nil {
			return displayErr
		}
	}
	return err
}

// Close the caller-owned store before exposing success; raw close errors are
// not public diagnostics. A late cancellation must not print exported.
func finishEvidenceExport(ctx context.Context, result managedworkspace.EvidenceResult, operationErr error, closeStore func() error, out io.Writer) error {
	if closeStore() != nil {
		result.State = "unknown_interrupted"
		operationErr = errors.Join(operationErr, managedworkspace.ErrArtifact)
	}
	if err := ctx.Err(); err != nil {
		if result.State == "exported" {
			result.State = "unknown_interrupted"
		}
		if !errors.Is(operationErr, err) {
			operationErr = errors.Join(operationErr, err)
		}
	}
	if err := writeManagedResult(out, fmt.Sprintf("Estado da exportação de evidências: %s\nArquivos: %d; bytes: %d\nConteúdo e patch: não incluídos\nPublicação na origem: não realizada\n", result.State, result.Files, result.Bytes)); err != nil {
		return errors.Join(operationErr, err)
	}
	return operationErr
}

func printManagedSummaries(out io.Writer, summaries []managedworkspace.Summary) error {
	data, err := json.Marshal(summaries)
	if err != nil {
		return errManagedOutput
	}
	return writeManagedResult(out, string(data)+"\n")
}

var errManagedOutput = errors.New("Falha ao exibir resultado gerenciado")

func printManagedReport(out io.Writer, report managedworkspace.Report) error {
	if report.Retention != nil {
		if err := writeManagedResult(out, fmt.Sprintf("Retenção privada: %s; pré-imagens: %d; conteúdo não exibido nem exportado.\n", report.Retention.State, report.Retention.Count)); err != nil {
			return err
		}
	}
	counts := map[string]int{}
	for _, op := range report.Operations {
		counts[op.Status]++
	}
	return writeManagedResult(out, fmt.Sprintf("Estado: %s\nOperações: %d; confirmadas: %d; negadas: %d; desconhecidas: %d; não iniciadas: %d\nPublicação na origem: não realizada\n", report.Status, len(report.Operations), counts["succeeded"], counts["denied"], counts["unknown"], counts["not_started"]))
}
func writeManagedResult(out io.Writer, text string) error {
	n, err := io.WriteString(out, text)
	if err != nil || n != len(text) {
		return errManagedOutput
	}
	return nil
}
func managedPlanError(err error) error {
	if errors.Is(err, workspaceplan.ErrInvalid) || errors.Is(err, workspaceplan.ErrBlocked) || errors.Is(err, workspaceplan.ErrLimit) {
		return &outputError{message: "Plano estruturado inválido, bloqueado ou acima do limite", cause: err}
	}
	return err
}
