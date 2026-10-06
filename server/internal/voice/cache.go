package voice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
)

// CachedSynthesizer는 정해진 글의 소리를 미리 만들어 두고, 그 글이 오면 공급자를 부르지 않고 바로 돌려준다.
//
// 가장 무거운 순간의 말(위기 고정 문구)은 모델도 합성 지연도 기다리지 않아야 한다. 첫 안부처럼 매번 나가는 말도 같은 길로 아낀다.
// 미리 만들지 않은 글은 그대로 공급자에 넘긴다.
type CachedSynthesizer struct {
	inner  Synthesizer
	logger *slog.Logger

	mu    sync.RWMutex
	ready map[string][]byte
}

var _ Synthesizer = (*CachedSynthesizer)(nil)

// NewCached는 캐시를 만든다. 미리 만드는 일은 Prewarm이 한다.
func NewCached(inner Synthesizer, logger *slog.Logger) (*CachedSynthesizer, error) {
	switch {
	case inner == nil:
		return nil, errors.New("voice: cached synthesizer needs an inner synthesizer")
	case logger == nil:
		return nil, errors.New("voice: cached synthesizer needs a logger")
	}
	return &CachedSynthesizer{inner: inner, logger: logger, ready: make(map[string][]byte)}, nil
}

// Prewarm은 글마다 소리를 만들어 둔다. 하나가 실패해도 나머지는 만든다. 실패한 글은 그때그때 공급자를 부른다.
// 서버가 뜰 때 뒤에서 돌리므로 글의 내용은 로그에 남기지 않고 수만 남긴다.
func (c *CachedSynthesizer) Prewarm(ctx context.Context, texts []string) {
	failed := 0
	for _, text := range texts {
		if ctx.Err() != nil {
			return
		}
		if c.has(text) {
			continue
		}
		audio, err := c.synthesizeAll(ctx, text)
		if err != nil {
			failed++
			continue
		}
		c.mu.Lock()
		c.ready[text] = audio
		c.mu.Unlock()
	}
	c.logger.LogAttrs(ctx, slog.LevelInfo, "fixed phrases synthesized",
		slog.Int("texts", len(texts)), slog.Int("failed", failed))
}

func (c *CachedSynthesizer) has(text string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.ready[text]
	return ok
}

func (c *CachedSynthesizer) synthesizeAll(ctx context.Context, text string) ([]byte, error) {
	stream, err := c.inner.Synthesize(ctx, text)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	audio, err := io.ReadAll(stream)
	if err != nil {
		return nil, err
	}
	if len(audio) == 0 {
		return nil, errors.New("voice: synthesizer returned no audio")
	}
	return audio, nil
}

// Synthesize는 미리 만들어 둔 소리가 있으면 그것을, 없으면 공급자의 소리를 돌려준다.
func (c *CachedSynthesizer) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	c.mu.RLock()
	audio, ok := c.ready[text]
	c.mu.RUnlock()
	if ok {
		return io.NopCloser(bytes.NewReader(audio)), nil
	}
	return c.inner.Synthesize(ctx, text)
}

func (c *CachedSynthesizer) SampleRate() int { return c.inner.SampleRate() }

// Cached는 미리 만들어 둔 글의 수다. 시험과 상태 확인에 쓴다.
func (c *CachedSynthesizer) Cached() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.ready)
}
