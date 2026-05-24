package embedding

import "math"

func CosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func NormalizeVector(v []float64) []float64 {
	var norm float64
	for _, x := range v {
		norm += x * x
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return v
	}
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = x / norm
	}
	return out
}

func MeanPooling(hidden [][][]float32, mask [][]int64) [][]float64 {
	batch := len(hidden)
	if batch == 0 {
		return nil
	}
	dim := len(hidden[0][0])
	result := make([][]float64, batch)
	for b := 0; b < batch; b++ {
		vec := make([]float64, dim)
		var sum float64
		for s := 0; s < len(hidden[b]); s++ {
			weight := float64(mask[b][s])
			sum += weight
			for d := 0; d < dim; d++ {
				vec[d] += float64(hidden[b][s][d]) * weight
			}
		}
		if sum > 0 {
			for d := 0; d < dim; d++ {
				vec[d] /= sum
			}
		}
		result[b] = vec
	}
	return result
}
