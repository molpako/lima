package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AlecAivazis/survey/v2"
	"github.com/lima-vm/lima/pkg/editutil"
	"github.com/lima-vm/lima/pkg/limayaml"
	networks "github.com/lima-vm/lima/pkg/networks/reconcile"
	"github.com/lima-vm/lima/pkg/start"
	"github.com/lima-vm/lima/pkg/store"
	"github.com/lima-vm/lima/pkg/store/filenames"
	"github.com/lima-vm/lima/pkg/yqutil"
	"github.com/mattn/go-isatty"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func newEditCommand() *cobra.Command {
	var editCommand = &cobra.Command{
		Use:               "edit INSTANCE",
		Short:             "Edit an instance of Lima",
		Args:              WrapArgsError(cobra.MaximumNArgs(1)),
		RunE:              editAction,
		ValidArgsFunction: editBashComplete,
	}
	// TODO: "survey" does not support using cygwin terminal on windows yet
	editCommand.Flags().Bool("tty", isatty.IsTerminal(os.Stdout.Fd()), "enable TUI interactions such as opening an editor, defaults to true when stdout is a terminal")
	editCommand.Flags().String("set", "", "modify the template inplace, using yq syntax")
	return editCommand
}

func editAction(cmd *cobra.Command, args []string) error {
	instName := DefaultInstanceName
	if len(args) > 0 {
		instName = args[0]
	}

	inst, err := store.Inspect(instName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("Instance %q not found", instName)
		}
		return err
	}

	if inst.Status == store.StatusRunning {
		return errors.New("Cannot edit a running instance")
	}

	filePath := filepath.Join(inst.Dir, filenames.LimaYAML)
	yContent, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	tty, err := cmd.Flags().GetBool("tty")
	if err != nil {
		return err
	}
	yq, err := cmd.Flags().GetString("set")
	if err != nil {
		return err
	}
	var yBytes []byte
	if yq != "" {
		logrus.Warn("`--set` is experimental")
		yBytes, err = yqutil.EvaluateExpression(yq, yContent)
		if err != nil {
			return err
		}
	} else if tty {
		hdr := fmt.Sprintf("# Please edit the following configuration for Lima instance %q\n", instName)
		hdr += "# and an empty file will abort the edit.\n"
		hdr += "\n"
		hdr += editutil.GenerateEditorWarningHeader()
		yBytes, err = editutil.OpenEditor(instName, yContent, hdr)
		if err != nil {
			return err
		}
	}
	if len(yBytes) == 0 {
		logrus.Info("Aborting, as requested by saving the file with empty content")
		return nil
	}
	if bytes.Compare(yBytes, yContent) == 0 {
		logrus.Info("Aborting, no changes made to the instance")
		return nil
	}
	y, err := limayaml.Load(yBytes, filePath)
	if err != nil {
		return err
	}
	if err := limayaml.Validate(*y, true); err != nil {
		rejectedYAML := "lima.REJECTED.yaml"
		if writeErr := os.WriteFile(rejectedYAML, yBytes, 0644); writeErr != nil {
			return fmt.Errorf("the YAML is invalid, attempted to save the buffer as %q but failed: %v: %w", rejectedYAML, writeErr, err)
		}
		// TODO: may need to support editing the rejected YAML
		return fmt.Errorf("the YAML is invalid, saved the buffer as %q: %w", rejectedYAML, err)
	}
	if err := os.WriteFile(filePath, yBytes, 0644); err != nil {
		return err
	}
	logrus.Infof("Instance %q configuration edited", instName)

	if !tty {
		// use "start" to start it
		return nil
	}
	startNow, err := askWhetherToStart()
	if err != nil {
		return err
	}
	if !startNow {
		return nil
	}
	ctx := cmd.Context()
	err = networks.Reconcile(ctx, inst.Name)
	if err != nil {
		return err
	}
	return start.Start(ctx, inst)
}

func askWhetherToStart() (bool, error) {
	ans := true
	prompt := &survey.Confirm{
		Message: "Do you want to start the instance now? ",
		Default: true,
	}
	if err := survey.AskOne(prompt, &ans); err != nil {
		return false, err
	}
	return ans, nil
}

func editBashComplete(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return bashCompleteInstanceNames(cmd)
}
