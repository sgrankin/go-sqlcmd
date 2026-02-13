package root

import (
	"github.com/sgrankin/go-sqlcmd/cmd/sqlcmd/root/plan"
	"github.com/sgrankin/go-sqlcmd/internal/cmdparser"
	"github.com/sgrankin/go-sqlcmd/internal/localizer"
)

// Plan defines the `sqlcmd plan` sub-commands
type Plan struct {
	cmdparser.Cmd
}

func (c *Plan) DefineCommand(...cmdparser.CommandOptions) {
	options := cmdparser.CommandOptions{
		Use:         "plan",
		Short:       localizer.Sprintf("Analyze SQL Server execution plans"),
		SubCommands: c.SubCommands(),
		Examples: []cmdparser.ExampleOptions{
			{
				Description: localizer.Sprintf("Analyze a saved execution plan"),
				Steps:       []string{"sqlcmd plan analyze plan.xml"},
			},
			{
				Description: localizer.Sprintf("Run a query and analyze its plan"),
				Steps:       []string{`sqlcmd plan analyze -Q "SELECT * FROM orders"`},
			},
		},
	}

	c.Cmd.DefineCommand(options)
}

func (c *Plan) SubCommands() []cmdparser.Command {
	dependencies := c.Dependencies()

	return []cmdparser.Command{
		cmdparser.New[*plan.Analyze](dependencies),
	}
}
