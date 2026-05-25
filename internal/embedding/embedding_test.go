package embedding

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCosineSimilarity(t *testing.T) {
	require.InDelta(t, 1.0, CosineSimilarity([]float64{1, 0}, []float64{1, 0}), 0.0001)
	require.InDelta(t, 0.0, CosineSimilarity([]float64{1, 0}, []float64{0, 1}), 0.0001)
	require.InDelta(t, 0.0, CosineSimilarity(nil, []float64{1, 0}), 0.0001)
}

func TestNormalizeVector(t *testing.T) {
	v := NormalizeVector([]float64{3, 4})
	require.InDelta(t, 0.6, v[0], 0.0001)
	require.InDelta(t, 0.8, v[1], 0.0001)

	zero := []float64{0, 0}
	require.Equal(t, zero, NormalizeVector(zero))
}

func TestMeanPooling(t *testing.T) {
	lastHidden := [][][]float32{
		{
			{1.0, 2.0, 3.0},
			{4.0, 5.0, 6.0},
			{7.0, 8.0, 9.0},
		},
	}
	mask := [][]int64{{1, 1, 0}}
	result := MeanPooling(lastHidden, mask)
	require.Len(t, result, 1)
	require.Len(t, result[0], 3)
	require.InDelta(t, 2.5, result[0][0], 0.001)
	require.InDelta(t, 3.5, result[0][1], 0.001)
	require.InDelta(t, 4.5, result[0][2], 0.001)
}

func TestCosineSimilarityDifferentLengths(t *testing.T) {
	require.InDelta(t, 0.0, CosineSimilarity([]float64{1, 0}, []float64{1, 0, 0}), 0.0001)
}

func TestCosineSimilarityZeroVector(t *testing.T) {
	require.InDelta(t, 0.0, CosineSimilarity([]float64{0, 0}, []float64{0, 0}), 0.0001)
}

func TestCosineSimilarityNegativeValues(t *testing.T) {
	require.InDelta(t, -1.0, CosineSimilarity([]float64{1, 0}, []float64{-1, 0}), 0.0001)
}

func TestMeanPoolingAllMasked(t *testing.T) {
	lastHidden := [][][]float32{
		{
			{1.0, 2.0, 3.0},
			{4.0, 5.0, 6.0},
		},
	}
	mask := [][]int64{{0, 0}}
	result := MeanPooling(lastHidden, mask)
	require.Len(t, result, 1)
	require.Len(t, result[0], 3)
	require.InDelta(t, 0.0, result[0][0], 0.001)
	require.InDelta(t, 0.0, result[0][1], 0.001)
	require.InDelta(t, 0.0, result[0][2], 0.001)
}

func TestMeanPoolingEmptyInput(t *testing.T) {
	result := MeanPooling(nil, nil)
	require.Nil(t, result)
	result = MeanPooling([][][]float32{}, [][]int64{})
	require.Nil(t, result)
}

func TestNormalizeVectorEmpty(t *testing.T) {
	v := NormalizeVector([]float64{})
	require.Empty(t, v)
}
