package observability

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestInitLoggerProduction(t *testing.T) {
	orig := Logger
	defer func() { Logger = orig }()

	require.NoError(t, InitLogger(true))
	require.NotNil(t, Logger)
}

func TestInitLoggerDevelopment(t *testing.T) {
	orig := Logger
	defer func() { Logger = orig }()

	require.NoError(t, InitLogger(false))
	require.NotNil(t, Logger)
}

func TestDefaultLoggerNoop(t *testing.T) {
	orig := Logger
	defer func() { Logger = orig }()

	Logger = zap.NewNop()
	Logger.Info("this should not panic")
}
