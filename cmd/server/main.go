package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/charmbracelet/log"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/tifye/Coconut/coconut"
	"golang.org/x/crypto/ssh"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		fmt.Printf("failed to read env files: %s\n", err)
	}

	logger := log.NewWithOptions(os.Stdout, log.Options{
		Level:           log.DebugLevel,
		TimeFormat:      "15:04:05",
		ReportTimestamp: true,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	cmd := newServerCommand(logger)
	err = cmd.ExecuteContext(ctx)
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
	ProxyAddr        string
	HostKeyPath      string
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

	cmd.Flags().StringVar(&opts.ClientListenAddr, "cla", "127.0.0.1:9000", "Address on which to listen for client connections")
	cmd.Flags().StringVar(&opts.ProxyAddr, "pa", "127.0.0.1:9999", "Address on which to host proxy")
	cmd.Flags().StringVar(&opts.HostKeyPath, "host-key", "", "Private key used for SSH host")

	return cmd
}

func runServer(ctx context.Context, logger *log.Logger, opts ServerOpts) error {
	hostKeyPath := os.Getenv("HOST_KEY")
	if opts.HostKeyPath != "" {
		hostKeyPath = opts.HostKeyPath
	}
	if hostKeyPath == "" {
		return fmt.Errorf("no host key path set")
	}

	signer, err := ssh.ParsePrivateKey(getBytes(hostKeyPath))
	if err != nil {
		return err
	}

	server, err := coconut.NewServer(
		logger.WithPrefix("server"),
		coconut.WithClientListenAddr(opts.ClientListenAddr),
		coconut.WithHostKey(signer),
		coconut.WithNoClientAuth(),
		coconut.WithProxyAddr(opts.ProxyAddr),
	)
	if err != nil {
		return fmt.Errorf("server create: %s", err)
	}

	err = server.Start(ctx)
	if err != nil {
		return fmt.Errorf("server start: %s", err)
	}

	logger.Info("server started")

	<-ctx.Done()

	errch := make(chan error)
	go func() {
		errch <- server.Close(context.Background())
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

func getBytes(path string) []byte {
	bts, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return bts
}
