package main

import (
	"fmt"
	"os"

	"github.com/SKAhack/ship/cmd"
	"github.com/spf13/cobra"
)

var (
	Version  string
	Revision string
)

const (
	cliName        = "ship"
	cliDescription = "deploy tool"
)

var rootCmd = &cobra.Command{
	Use:        cliName,
	Short:      cliDescription,
	SuggestFor: []string{"ship"},
}

func main() {
	rootCmd.AddCommand(
		cmd.NewDeployCommand(os.Stdout, os.Stderr),
		cmd.NewRollbackCommand(os.Stdout, os.Stderr),
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(-1)
	}
}
