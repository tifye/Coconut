package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"github.com/tifye/Coconut/coconut"
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
	if errors.Is(err, context.Canceled) {
		logger.Info("command exited via context cancellation")
		return
	}
	if err != nil {
		logger.Error("error executing server command", "err", err)
	}
}

type ServerOpts struct {
	ClientListenAddr string
}

func newServerCommand(logger *log.Logger) *cobra.Command {
	opts := ServerOpts{}
	cmd := &cobra.Command{
		Use:          "coconut",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServer(cmd.Context(), logger, opts)
		},
	}

	cmd.Flags().StringVar(&opts.ClientListenAddr, "cla", "127.0.0.1:9000", "Address on which to listen for client connections.")

	return cmd
}

func runServer(ctx context.Context, logger *log.Logger, opts ServerOpts) error {
	config := coconut.ServerConfig{
		ClientListenAddr: opts.ClientListenAddr,
	}
	server := coconut.NewServer(&config, logger.WithPrefix("server"))
	err := server.Start()
	if err != nil {
		return fmt.Errorf("server start: %s", err)
	}

	logger.Info("server started")

	<-ctx.Done()

	errch := make(chan error)
	go func() {
		errch <- server.Close()
	}()

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case <-shutdownCtx.Done():
		return fmt.Errorf("server shutdown: %w", shutdownCtx.Err())
	case err := <-errch:
		if err == nil {
			return nil
		} else {
			return fmt.Errorf("server shutdown: %w", err)
		}
	}
}
