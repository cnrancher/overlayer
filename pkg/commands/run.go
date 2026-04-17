package commands

import (
	"fmt"

	"github.com/cnrancher/overlayer/pkg/config"
	"github.com/cnrancher/overlayer/pkg/server"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type runOpts struct {
	ConfigFile string
}

type runCmd struct {
	*baseCmd
	*runOpts

	server server.Server
	config *config.Config
}

func newRunCmd() *runCmd {
	cc := &runCmd{
		runOpts: &runOpts{},
	}
	cc.baseCmd = newBaseCmd(&cobra.Command{
		Use:  "run",
		Long: "Run the proxy server",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if cc.debug {
				logrus.SetLevel(logrus.DebugLevel)
				logrus.Debugf("Debug mode enabled")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cc.init(); err != nil {
				return fmt.Errorf("failed to initialize run command: %w", err)
			}
			return cc.run()
		},
	})
	flags := cc.cmd.Flags()
	flags.StringVarP(&cc.ConfigFile, "config", "c", "config.yaml", "Config file")

	return cc
}

func (cc *runCmd) init() error {
	var err error
	if cc.ConfigFile == "" {
		return fmt.Errorf("config file not provided")
	}
	cc.config, err = config.NewConfigFromFile(cc.ConfigFile)
	if err != nil {
		return err
	}

	cc.server, err = server.NewRegistryServer(signalContext, cc.config)
	if err != nil {
		return fmt.Errorf("failed to create proxy server: %w", err)
	}
	return nil
}

func (cc *runCmd) run() error {
	return cc.server.Serve(signalContext)
}

func (cc *runCmd) getCommand() *cobra.Command {
	return cc.cmd
}
