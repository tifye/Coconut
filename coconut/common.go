package coconut

import (
	"errors"
	"io"

	"golang.org/x/crypto/ssh"
)

// tunnel represents a channel of communication between server and client.
// Communication is done over an SSH Channel
type tunnel struct {
	sshChan io.ReadWriteCloser
	reqs    <-chan *ssh.Request
}

// Closes the SSH Channel used by this tunnel.
//
// This funciton will return nil error if it finds the underlying
// network connection has already been closed (by reading an EOF).
// This may change in the future.
func (t *tunnel) Close() error {
	err := t.sshChan.Close()
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
