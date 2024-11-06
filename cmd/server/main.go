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

	cmd := newClientCommand(logger)
	err := cmd.ExecuteContext(ctx)
	if err != nil {
		logger.Error("error executing server command", "err", err)
	}
}

func newClientCommand(logger *log.Logger) *cobra.Command {
	cmd := &cobra.Command{
		Use: "coconut",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServer(logger)
		},
	}
	return cmd
}

func runServer(logger *log.Logger) error {
	logger.Print("runClient")
	return nil
}
