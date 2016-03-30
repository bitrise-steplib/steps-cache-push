package main

import (
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_sandbox(t *testing.T) {
	{
		res := strings.TrimSuffix("a//", "/")
		require.Equal(t, "a/", res)
	}

	{
		res := path.Clean("a//")
		require.Equal(t, "a", res)
	}
}
