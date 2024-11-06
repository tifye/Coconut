package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

func main() {
	logger := log.NewWithOptions(os.Stdout, log.Options{
		Level:           log.DebugLevel,
		TimeFormat:      "15:04:05",
		ReportTimestamp: true,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	cmd := newServerCommand(logger)
	err := cmd.ExecuteContext(ctx)
	if err != nil {
		logger.Error("error executing server command", "err", err)
	}
}

func newServerCommand(logger *log.Logger) *cobra.Command {
	cmd := &cobra.Command{
		Use: "coconut",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClient(logger)
		},
	}
	return cmd
}

func runClient(logger *log.Logger) error {
	logger.Print("runServer")
	return nil
}
