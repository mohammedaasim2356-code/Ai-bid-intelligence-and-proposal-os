package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Doc is a retrievable unit (a knowledge chunk or a library question).
type Doc struct {
	ID    string
	Text  string
	terms []string
	vec   []float32
}

// BM25 ranks docs lexically. Built per query set from in-memory docs; the demo corpus is
// a few hundred chunks, so rebuilding is milliseconds.
// ponytail: in-process BM25 over all org chunks; swap for FTS5/tsvector behind the same
// Rank signature when corpora reach tens of thousands of chunks.
type BM25 struct {
	docs   []Doc
	df     map[string]int
	avgLen float64
	k1, b  float64
}

func NewBM25(docs []Doc, aliases []string) *BM25 {
	idx := &BM25{docs: docs, df: map[string]int{}, k1: 1.2, b: 0.75}
	total := 0
	for i := range idx.docs {
		idx.docs[i].terms = Terms(idx.docs[i].Text, aliases)
		total += len(idx.docs[i].terms)
		seen := map[string]bool{}
		for _, t := range idx.docs[i].terms {
			if !seen[t] {
				seen[t] = true
				idx.df[t]++
			}
		}
	}
	if len(docs) > 0 {
		idx.avgLen = float64(total) / float64(len(docs))
	}
	return idx
}

type Hit struct {
	ID    string
	Score float64
}

// Rank scores docs for the query terms (stem-family matching) and returns hits sorted desc.
func (idx *BM25) Rank(queryTerms []string, limit int) []Hit {
	n := float64(len(idx.docs))
	var hits []Hit
	for _, d := range idx.docs {
		score := 0.0
		dl := float64(len(d.terms))
		for _, q := range queryTerms {
			tf := 0
			for _, t := range d.terms {
				if StemMatch(q, t) {
					tf++
				}
			}
			if tf == 0 {
				continue
			}
			df := float64(idx.dfFamily(q))
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			score += idf * (float64(tf) * (idx.k1 + 1)) / (float64(tf) + idx.k1*(1-idx.b+idx.b*dl/math.Max(idx.avgLen, 1)))
		}
		if score > 0 {
			hits = append(hits, Hit{ID: d.ID, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func (idx *BM25) dfFamily(q string) int {
	if v, ok := idx.df[q]; ok {
		return v
	}
	// fall back to family matches (rare path)
	c := 0
	for t, v := range idx.df {
		if StemMatch(q, t) {
			c += v
		}
	}
	if c == 0 {
		return 1
	}
	return c
}

// Embedder produces vectors for texts.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	ModelID() string
}

// HashEmbedder is a deterministic, dependency-free embedding: feature-hashed word and
// character n-grams, L2-normalised. It captures lexical similarity with tolerance for
// morphology; it is the offline floor, not a semantic model.
type HashEmbedder struct{ Dim int }

func (h HashEmbedder) ModelID() string { return fmt.Sprintf("hash-ngram-%d", h.dim()) }

func (h HashEmbedder) dim() int {
	if h.Dim <= 0 {
		return 512
	}
	return h.Dim
}

func (h HashEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = h.vector(t)
	}
	return out, nil
}

func (h HashEmbedder) vector(text string) []float32 {
	dim := h.dim()
	v := make([]float32, dim)
	terms := Terms(text, nil)
	add := func(feature string, w float32) {
		f := fnv.New32a()
		f.Write([]byte(feature))
		x := f.Sum32()
		idx := int(x % uint32(dim))
		sign := float32(1)
		if x&0x80000000 != 0 {
			sign = -1
		}
		v[idx] += sign * w
	}
	for i, t := range terms {
		add("w:"+t, 1)
		if i+1 < len(terms) {
			add("b:"+t+"_"+terms[i+1], 0.5)
		}
		r := []rune(t)
		for j := 0; j+3 <= len(r); j++ {
			add("c:"+string(r[j:j+3]), 0.3)
		}
	}
	var norm float64
	for _, x := range v {
		norm += float64(x * x)
	}
	if norm > 0 {
		n := float32(math.Sqrt(norm))
		for i := range v {
			v[i] /= n
		}
	}
	return v
}

// RemoteEmbedder calls an OpenAI-compatible /embeddings endpoint.
type RemoteEmbedder struct {
	BaseURL, Model, APIKey string
	Client                 *http.Client
}

func (r RemoteEmbedder) ModelID() string { return "remote:" + r.Model }

func (r RemoteEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": r.Model, "input": texts})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(r.BaseURL, "/")+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.APIKey)
	}
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embeddings endpoint returned %s", resp.Status)
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index >= 0 && d.Index < len(vecs) {
			vecs[d.Index] = d.Embedding
		}
	}
	return vecs, nil
}

// Cosine similarity of two vectors (0 when dimensions differ).
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// RRF fuses ranked lists with Reciprocal Rank Fusion (k = 60).
func RRF(lists ...[]Hit) []Hit {
	const k = 60.0
	scores := map[string]float64{}
	order := []string{}
	for _, list := range lists {
		for rank, h := range list {
			if _, ok := scores[h.ID]; !ok {
				order = append(order, h.ID)
			}
			scores[h.ID] += 1.0 / (k + float64(rank+1))
		}
	}
	out := make([]Hit, 0, len(order))
	for _, id := range order {
		out = append(out, Hit{ID: id, Score: scores[id]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}
