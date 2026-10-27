// Command llmention measures how often AI assistants mention your product
// when buyers ask them for recommendations.
package main

import (
	"os"

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

func main() {
	os.Exit(newApp().Run(os.Args[1:], os.Stdout, os.Stderr))
}
