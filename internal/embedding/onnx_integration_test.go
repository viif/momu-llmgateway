package embedding

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestONNXEmbedding(t *testing.T) {
	modelPath := os.Getenv("EMBEDDING_MODEL_PATH")
	if modelPath == "" {
		modelPath = "../../.models/bge-small-zh-v1.5"
	}
	libPath := os.Getenv("ONNXRUNTIME_LIB_PATH")
	if libPath == "" {
		libPath = "/usr/local/lib/libonnxruntime.so.1.25.0"
	}

	err := Init(libPath, modelPath)
	require.NoError(t, err)
	defer func() {
		if e := Instance(); e != nil {
			e.Close()
		}
	}()

	vecs, err := Instance().Embed([]string{"你好世界", "Hello world"})
	require.NoError(t, err)
	require.Len(t, vecs, 2)
	require.Len(t, vecs[0], 512)

	norm := 0.0
	for _, v := range vecs[0] {
		norm += v * v
	}
	require.InDelta(t, 1.0, norm, 0.01)
}
