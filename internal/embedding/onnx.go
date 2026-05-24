package embedding

import (
	"fmt"
	"os"

	"github.com/gomlx/go-huggingface/tokenizers/api"
	"github.com/gomlx/go-huggingface/tokenizers/hftokenizer"
	ort "github.com/yalue/onnxruntime_go"
)

type onnxConcrete struct {
	tokenizer *hftokenizer.Tokenizer
	session   *ort.DynamicAdvancedSession
}

func Init(libPath, modelPath string) error {
	once.Do(func() {
		ort.SetSharedLibraryPath(libPath)
		if err := ort.InitializeEnvironment(); err != nil {
			initErr = fmt.Errorf("init onnx env: %w", err)
			return
		}

		configData, err := os.ReadFile(modelPath + "/tokenizer_config.json")
		if err != nil {
			initErr = fmt.Errorf("read tokenizer_config.json: %w", err)
			return
		}
		config, err := api.ParseConfigContent(configData)
		if err != nil {
			initErr = fmt.Errorf("parse tokenizer config: %w", err)
			return
		}

		tk, err := hftokenizer.NewFromFile(config, modelPath+"/tokenizer.json")
		if err != nil {
			initErr = fmt.Errorf("load tokenizer: %w", err)
			return
		}

		session, err := ort.NewDynamicAdvancedSession(
			modelPath+"/model.onnx",
			[]string{"input_ids", "attention_mask", "token_type_ids"},
			[]string{"last_hidden_state"},
			nil,
		)
		if err != nil {
			initErr = fmt.Errorf("load onnx session: %w", err)
			return
		}

		impl := &onnxConcrete{
			tokenizer: tk,
			session:   session,
		}
		engine = &EmbeddingEngine{
			onnxImpl:     impl,
			maxLength:    defaultMaxLength,
			embeddingDim: defaultEmbeddingDim,
		}
	})
	return initErr
}

func (e *EmbeddingEngine) Embed(texts []string) ([][]float64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.onnxImpl == nil {
		return nil, fmt.Errorf("onnx engine not initialized")
	}
	return e.onnxImpl.embed(texts)
}

func (o *onnxConcrete) embed(texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	batchSize := int64(len(texts))

	inputIDs := make([]int64, batchSize*defaultMaxLength)
	attnMask := make([]int64, batchSize*defaultMaxLength)
	typeIDs := make([]int64, batchSize*defaultMaxLength)

	for b, text := range texts {
		tokenIDs := o.tokenizer.Encode(text)
		ids, mask := padAndMaskInt(tokenIDs, int(defaultMaxLength))
		base := int64(b) * defaultMaxLength
		copy(inputIDs[base:base+defaultMaxLength], ids)
		copy(attnMask[base:base+defaultMaxLength], mask)
	}

	inputShape := ort.NewShape(batchSize, defaultMaxLength)
	inputTensor, err := ort.NewTensor(inputShape, inputIDs)
	if err != nil {
		return nil, fmt.Errorf("create input_ids tensor: %w", err)
	}
	defer func() { _ = inputTensor.Destroy() }()

	maskTensor, err := ort.NewTensor(inputShape, attnMask)
	if err != nil {
		return nil, fmt.Errorf("create attention_mask tensor: %w", err)
	}
	defer func() { _ = maskTensor.Destroy() }()

	typeTensor, err := ort.NewTensor(inputShape, typeIDs)
	if err != nil {
		return nil, fmt.Errorf("create token_type_ids tensor: %w", err)
	}
	defer func() { _ = typeTensor.Destroy() }()

	outputShape := ort.NewShape(batchSize, defaultMaxLength, defaultEmbeddingDim)
	outputTensor, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		return nil, fmt.Errorf("create output tensor: %w", err)
	}
	defer func() { _ = outputTensor.Destroy() }()

	inputs := []ort.Value{inputTensor, maskTensor, typeTensor}
	outputs := []ort.Value{outputTensor}
	if err := o.session.Run(inputs, outputs); err != nil {
		return nil, fmt.Errorf("onnx inference: %w", err)
	}

	rawOutput := outputTensor.GetData()
	hidden := reshapeHidden(rawOutput, int(batchSize), int(defaultMaxLength), int(defaultEmbeddingDim))

	attnMask2D := reshapeMask(attnMask, int(batchSize), int(defaultMaxLength))
	pooled := MeanPooling(hidden, attnMask2D)

	for i := range pooled {
		pooled[i] = NormalizeVector(pooled[i])
	}

	return pooled, nil
}

func (o *onnxConcrete) close() {
	if o.session != nil {
		_ = o.session.Destroy()
	}
}

func padAndMaskInt(ids []int, maxLen int) ([]int64, []int64) {
	seqLen := len(ids)
	if seqLen > maxLen {
		seqLen = maxLen
	}
	fullIDs := make([]int64, maxLen)
	fullMask := make([]int64, maxLen)
	for i := 0; i < seqLen; i++ {
		fullIDs[i] = int64(ids[i])
		fullMask[i] = 1
	}
	return fullIDs, fullMask
}

func reshapeHidden(flat []float32, batch, seqLen, dim int) [][][]float32 {
	out := make([][][]float32, batch)
	for b := 0; b < batch; b++ {
		out[b] = make([][]float32, seqLen)
		for s := 0; s < seqLen; s++ {
			out[b][s] = make([]float32, dim)
			base := (b*seqLen + s) * dim
			copy(out[b][s], flat[base:base+dim])
		}
	}
	return out
}

func reshapeMask(flat []int64, batch, seqLen int) [][]int64 {
	out := make([][]int64, batch)
	for b := 0; b < batch; b++ {
		out[b] = make([]int64, seqLen)
		copy(out[b], flat[b*seqLen:(b+1)*seqLen])
	}
	return out
}
