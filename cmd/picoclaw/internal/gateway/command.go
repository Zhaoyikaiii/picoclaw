package gateway

import (
	"github.com/spf13/cobra"
)

func NewGatewayCommand() *cobra.Command {
	var debug bool
	var configPath string

	cmd := &cobra.Command{
		Use:     "gateway",
		Aliases: []string{"g"},
		Short:   "Start picoclaw gateway",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return gatewayCmd(debug, configPath)
		},
	}

	cmd.Flags().BoolVarP(&debug, "debug", "d", false, "Enable debug logging")
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to config file")

	return cmd
}
