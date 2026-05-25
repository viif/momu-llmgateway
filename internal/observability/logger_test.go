package observability

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestInitLoggerProduction(t *testing.T) {
	require.NoError(t, InitLogger(true))
	require.NotNil(t, Logger)
	Logger.Info("test production log")
}

func TestInitLoggerDevelopment(t *testing.T) {
	require.NoError(t, InitLogger(false))
	require.NotNil(t, Logger)
	Logger.Debug("test development log")
}

func TestDefaultLoggerNoop(t *testing.T) {
	Logger = zap.NewNop()
	Logger.Info("this should not panic")
}
