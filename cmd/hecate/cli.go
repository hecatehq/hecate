package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/hecatehq/hecate/internal/version"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func newRootCommand() *cobra.Command {
	var printVersion bool

	root := &cobra.Command{
		Use:           "hecate",
		Short:         "Local AI runtime console",
		SilenceUsage:  true,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, args []string) {
			if printVersion {
				printVersionString(cmd.OutOrStdout())
				return
			}
			runServe()
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.Flags().BoolVarP(&printVersion, "version", "v", false, "print the version and exit")

	root.AddCommand(newServeCommand())
	root.AddCommand(newVersionCommand())
	root.AddCommand(newMCPCommand())
	root.AddCommand(newLegacyMCPServerCommand())

	return root
}

func newServeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the Hecate runtime",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runServe()
		},
	}
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Hecate version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			printVersionString(cmd.OutOrStdout())
		},
	}
}

func newMCPCommand() *cobra.Command {
	mcp := &cobra.Command{
		Use:   "mcp",
		Short: "Run Hecate protocol surfaces",
	}
	mcp.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Start the Hecate MCP server over stdio",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runMCPServer("hecate mcp serve")
		},
	})
	return mcp
}

func newLegacyMCPServerCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "mcp-server",
		Short:  "Start the Hecate MCP server over stdio",
		Hidden: true,
		Args:   cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runMCPServer("hecate mcp-server")
		},
	}
}

func printVersionString(w io.Writer) {
	fmt.Fprintln(w, version.Version)
}
