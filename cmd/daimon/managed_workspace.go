package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/netty-linux/daimon/internal/editcontract"
	"github.com/netty-linux/daimon/internal/managedworkspace"
	"github.com/netty-linux/daimon/internal/workspaceplan"
)

var errManagedArguments = errors.New("Uso: managed-workspace --base <store-privado> create --source <origem> | apply --run <id> --plan <arquivo> | report --run <id>")

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
	case "report":
		allowed["--run"] = true
	default:
		return "", "", nil, errManagedArguments
	}
	values := map[string]string{}
	for i := 3; i < len(args); i += 2 {
		if i+1 >= len(args) || !allowed[args[i]] || values[args[i]] != "" || args[i+1] == "" {
			return "", "", nil, errManagedArguments
		}
		values[args[i]] = args[i+1]
	}
	if len(values) != len(allowed) {
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
	p, err := r.Prepare(ctx, plan)
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

var errManagedOutput = errors.New("Falha ao exibir resultado gerenciado")

func printManagedReport(out io.Writer, report managedworkspace.Report) error {
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
