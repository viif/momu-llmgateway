package embedding

import "sync"

type EmbeddingEngine struct {
	mu           sync.Mutex
	maxLength    int64
	embeddingDim int64
	// onnxImpl holds the CGO-dependent implementation; nil if ONNX unavailable.
	onnxImpl interface {
		embed(texts []string) ([][]float64, error)
		close()
	}
}

var (
	once    sync.Once
	engine  *EmbeddingEngine
	initErr error
)

const (
	defaultMaxLength    = 512
	defaultEmbeddingDim = 512
)

func Instance() *EmbeddingEngine {
	return engine
}

func (e *EmbeddingEngine) Close() {
	if e.onnxImpl != nil {
		e.onnxImpl.close()
	}
}
