package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

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
		RunE: func(cmd *cobra.Command, args []string) error {
			if printVersion {
				printVersionString(cmd.OutOrStdout())
				return nil
			}
			return runInteractiveShell(cmd.InOrStdin(), cmd.OutOrStdout())
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

func runInteractiveShell(in io.Reader, out io.Writer) error {
	fmt.Fprintln(out, "Hecate operator shell")
	fmt.Fprintln(out, "Type help for commands. Use `hecate serve` to start the runtime.")
	fmt.Fprintln(out)

	scanner := bufio.NewScanner(in)
	for {
		fmt.Fprint(out, "hecate> ")
		if !scanner.Scan() {
			fmt.Fprintln(out)
			return scanner.Err()
		}
		switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
		case "", "status":
			fmt.Fprintln(out, "Runtime status is available from the browser UI or `hecate serve` logs.")
		case "help", "h", "?":
			fmt.Fprintln(out, "Commands: status, serve, ui, help, quit")
		case "serve":
			fmt.Fprintln(out, "Start the runtime with: hecate serve")
		case "ui":
			fmt.Fprintln(out, "Start the runtime with `hecate serve`, then open http://127.0.0.1:8765")
		case "quit", "exit", "q":
			fmt.Fprintln(out, "bye")
			return nil
		default:
			fmt.Fprintln(out, "Unknown command. Type help for commands.")
		}
	}
}
