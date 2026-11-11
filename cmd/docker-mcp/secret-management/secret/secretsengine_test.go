package secret

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDialSecretsEngineFallsBackToLaterPath(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.sock")
	available := filepath.Join(dir, "available.sock")

	listener, err := net.Listen("unix", available)
	require.NoError(t, err)
	defer listener.Close()

	conn, err := dialSecretsEngine(missing, available)(context.Background(), "unix", "")
	require.NoError(t, err)
	conn.Close()
}

func TestDialSecretsEnginePrefersEarlierPath(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.sock")
	second := filepath.Join(dir, "second.sock")

	firstListener, err := net.Listen("unix", first)
	require.NoError(t, err)
	defer firstListener.Close()
	secondListener, err := net.Listen("unix", second)
	require.NoError(t, err)
	defer secondListener.Close()

	conn, err := dialSecretsEngine(first, second)(context.Background(), "unix", "")
	require.NoError(t, err)
	defer conn.Close()

	// The connection must land on the first listener.
	accepted, err := firstListener.Accept()
	require.NoError(t, err)
	accepted.Close()
}

func TestDialSecretsEngineErrorsWhenNoneAvailable(t *testing.T) {
	dir := t.TempDir()

	_, err := dialSecretsEngine(filepath.Join(dir, "a.sock"), filepath.Join(dir, "b.sock"))(context.Background(), "unix", "")
	assert.Error(t, err)
}
