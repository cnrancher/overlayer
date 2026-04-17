package commands

import (
	"fmt"

	"github.com/cnrancher/overlayer/pkg/utils"
	"github.com/spf13/cobra"
)

type versionCmd struct {
	*baseCmd
}

func newVersionCmd() *versionCmd {
	cc := &versionCmd{}
	cc.baseCmd = newBaseCmd(&cobra.Command{
		Use:  "version",
		Long: "Show version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("checker version %s\n", cc.version())
		},
	})

	return cc
}

func (cc *versionCmd) version() string {
	if utils.Commit != "" {
		return fmt.Sprintf("%v - %v", utils.Version, utils.Commit)
	}
	return utils.Version
}

func (cc *versionCmd) getCommand() *cobra.Command {
	return cc.cmd
}
