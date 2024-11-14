package testutil

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func GetBytes(tb testing.TB, path string) []byte {
	tb.Helper()
	bts, err := os.ReadFile(path)
	require.Nil(tb, err)
	return bts
}
