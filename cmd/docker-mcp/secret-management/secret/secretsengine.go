package secret

import (
	"context"
	"errors"
	"net"

	"github.com/docker/secrets-engine/client"
	"github.com/docker/secrets-engine/client/realms"
	"github.com/docker/secrets-engine/x/api"
)

// ErrSecretNotFound is returned when a requested secret does not exist.
// It aliases the SDK's not-found error so callers can use errors.Is against either.
var ErrSecretNotFound = client.ErrSecretNotFound

// newClient builds a Secrets Engine client.
//
// The engine listens on a different socket depending on how it was installed:
// Docker Desktop exposes it at api.DefaultSocketPath() (engine.sock), while the
// standalone engine used by Docker CE listens at api.DaemonSocketPath() (an
// abstract socket on Linux). Dial the standalone socket first and fall back to
// the Docker Desktop socket so a single build works with both installations.
func newClient() (client.Client, error) {
	return client.New(client.WithDialContext(dialSecretsEngine(api.DaemonSocketPath(), api.DefaultSocketPath())))
}

// dialSecretsEngine returns a dial function that connects to the first socket
// path that accepts a connection. It mirrors the SDK's own unix dialer so the
// behaviour is identical to client.WithSocketPath.
func dialSecretsEngine(paths ...string) func(ctx context.Context, _, _ string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		var errs []error
		for _, path := range paths {
			conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
			if err == nil {
				return conn, nil
			}
			errs = append(errs, err)
		}
		return nil, errors.Join(errs...)
	}
}

// GetSecrets returns all secrets under the docker/mcp/** realm.
func GetSecrets(ctx context.Context) ([]client.Envelope, error) {
	c, err := newClient()
	if err != nil {
		return nil, err
	}

	envelopes, err := c.GetSecrets(ctx, realms.DockerMCPDefault)
	if errors.Is(err, ErrSecretNotFound) {
		return []client.Envelope{}, nil
	}
	if err != nil {
		return nil, err
	}
	return envelopes, nil
}

// GetSecret retrieves a single secret by its full ID (e.g., "docker/mcp/oauth/github").
// Returns ErrSecretNotFound if the secret does not exist.
func GetSecret(ctx context.Context, id client.ID) (*client.Envelope, error) {
	pattern, err := client.ParsePattern(id.String())
	if err != nil {
		return nil, err
	}

	c, err := newClient()
	if err != nil {
		return nil, err
	}

	envelopes, err := c.GetSecrets(ctx, pattern)
	if errors.Is(err, ErrSecretNotFound) {
		return nil, ErrSecretNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(envelopes) == 0 {
		return nil, ErrSecretNotFound
	}
	return &envelopes[0], nil
}
