package commands

import (
	"github.com/cnrancher/overlayer/pkg/utils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func Execute(args []string) {
	proxyCmd := newProxyCmd()
	proxyCmd.addCommands()
	proxyCmd.cmd.SetArgs(args)

	_, err := proxyCmd.cmd.ExecuteC()
	if err != nil {
		if signalContext.Err() != nil {
			logrus.Fatal(signalContext.Err())
		}
		logrus.Fatal(err)
	}
}

type proxyCmd struct {
	*baseCmd
}

func newProxyCmd() *proxyCmd {
	cc := &proxyCmd{}
	cc.baseCmd = newBaseCmd(&cobra.Command{
		Use:   "proxy",
		Short: "Run registry server reverse proxy",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
		},
	})
	cc.cmd.Version = utils.Version
	cc.cmd.SilenceUsage = true
	cc.cmd.SilenceErrors = true

	flags := cc.cmd.PersistentFlags()
	flags.BoolVarP(&cc.baseCmd.debug, "debug", "", false, "enable debug output")

	return cc
}

func (cc *proxyCmd) getCommand() *cobra.Command {
	return cc.cmd
}

func (cc *proxyCmd) addCommands() {
	addCommands(
		cc.cmd,
		newRunCmd(),
		newVersionCmd(),
	)
}
