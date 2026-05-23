package observability

import "go.uber.org/zap"

var Logger *zap.Logger = zap.NewNop()

func InitLogger(production bool) error {
	var err error
	if production {
		Logger, err = zap.NewProduction()
	} else {
		Logger, err = zap.NewDevelopment()
	}
	return err
}
