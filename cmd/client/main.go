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
	ServerAddr  string
	ProxyToAddr string
	SigningKey  string
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

	cmd.Flags().StringVar(&opts.ServerAddr, "server-addr", "127.0.0.1:9000", "Address on which server listens for client connections.")
	cmd.Flags().StringVar(&opts.ProxyToAddr, "proxy-addr", "127.0.0.1:3000", "Address to which to proxy request.")
	cmd.Flags().StringVar(&opts.SigningKey, "signing-key", "", "The key to use when signing client.")

	return cmd
}

func runClient(ctx context.Context, logger *log.Logger, opts ClientOpts) error {
	client, err := coconut.NewClient(
		logger.WithPrefix("client"),
		opts.ServerAddr,
		opts.ProxyToAddr,
		coconut.WithHostKeyCallback(ssh.InsecureIgnoreHostKey()),
		coconut.WithAuthMethod(ssh.PublicKeysCallback(func() (signers []ssh.Signer, err error) {
			s, err := getSigner(opts.SigningKey)
			if err != nil {
				return nil, err
			}
			return []ssh.Signer{s}, nil
		})),
		coconut.WithUser("tifye"),
		coconut.WithBannerCallback(func(message string) error {
			logger.Print(message)
			return nil
		}),
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

func getSigner(spath string) (ssh.Signer, error) {
	rawKey, err := os.ReadFile(spath)
	if err != nil {
		return nil, err
	}

	signer, err := ssh.ParsePrivateKey([]byte(rawKey))
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key bytes, got: %s", err)
	}

	return signer, nil
}
