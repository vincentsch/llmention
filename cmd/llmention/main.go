// Command llmention measures how often AI assistants mention your product
// when buyers ask them for recommendations.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	rungrad "github.com/vincentsch/rungrad"

	"github.com/vincentsch/llmention/internal/version"
)

func newApp() *rungrad.App {
	app := rungrad.New(rungrad.AppConfig{
		Name:           "llmention",
		Short:          "See how often AI assistants mention your product",
		Long:           "llmention asks AI assistants the questions your buyers ask, using your own API keys. It reports how often each assistant mentions you next to your competitors, and which websites it cites.",
		Version:        version.String(),
		AdvancedOutput: true,
	})
	root := app.Root()
	root.Example = "llmention demo\nllmention quick --brand Acme --category \"invoicing for freelancers\" --competitors \"Globex,Initech\" --engines openai,anthropic\nllmention init\nllmention run\nllmention report\nllmention report --trend"
	app.AddGroup(
		rungrad.Group{ID: "start", Title: "Get Started"},
		rungrad.Group{ID: "measure", Title: "Measure"},
		rungrad.Group{ID: "manage", Title: "Manage"},
	)
	app.AddCommand(demoCommand(), quickCommand(), initCommand())
	app.AddCommand(runCommand(), reportCommand())
	app.AddCommand(discoverCommand())
	app.AddCommand(historyCommand(), suitesCommand(), updateCommand())
	return app
}

// The argument checks below replace cobra's "accepts 1 arg(s), received 0",
// which doesn't say what is missing.

// noArgs refuses arguments for a command that only takes flags.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%s takes no arguments, but got %q; `%s --help` lists its flags", cmd.CommandPath(), strings.Join(args, " "), cmd.CommandPath())
	}
	return nil
}

// oneArg asks for one argument, such as "run id". When it is missing, the
// error names it, shows an example and says where to find a value.
func oneArg(name, example, where string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		switch {
		case len(args) == 0:
			return fmt.Errorf("%s needs a %s, for example `%s`. %s", cmd.CommandPath(), name, example, where)
		case len(args) > 1:
			return fmt.Errorf("%s takes one %s, but got %d: %s", cmd.CommandPath(), name, len(args), strings.Join(args, " "))
		}
		return nil
	}
}

func main() {
	os.Exit(newApp().Run(os.Args[1:], os.Stdout, os.Stderr))
}
