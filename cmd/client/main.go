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
	"golang.org/x/crypto/ssh"
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
	if errors.Is(err, context.Canceled) {
		logger.Info("command exited via context cancellation")
		return
	}
	if err != nil {
		logger.Error("error executing client command", "err", err)
	}
}

type ClientOpts struct {
	ServerAddr string
}

func newClientCommand(logger *log.Logger) *cobra.Command {
	opts := ClientOpts{}
	cmd := &cobra.Command{
		Use:          "coconut",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClient(cmd.Context(), logger, opts)
		},
	}

	cmd.Flags().StringVar(&opts.ServerAddr, "saddr", "127.0.0.1:9000", "Address on which server listens for client connections.")

	return cmd
}

func runClient(ctx context.Context, logger *log.Logger, opts ClientOpts) error {
	client, err := coconut.NewClient(
		logger.WithPrefix("client"),
		opts.ServerAddr,
		coconut.WithHostKeyCallback(ssh.InsecureIgnoreHostKey()),
		coconut.WithUser("tifye"),
	)
	if err != nil {
		return fmt.Errorf("client create: %s", err)
	}

	err = client.Start()
	if err != nil {
		return fmt.Errorf("client start: %s", err)
	}

	logger.Info("client started")

	<-ctx.Done()

	errch := make(chan error)
	go func() {
		errch <- client.Close()
	}()

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case <-shutdownCtx.Done():
		return fmt.Errorf("client shutdown: %w", shutdownCtx.Err())
	case err := <-errch:
		if err == nil {
			return nil
		} else {
			return fmt.Errorf("client shutdown: %w", err)
		}
	}
}
