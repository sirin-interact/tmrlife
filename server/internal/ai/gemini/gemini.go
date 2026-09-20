// Package gemini는 Gemini 모델을 ai.LLM과 ai.StreamLLM으로 감싼다.
//
// 공급자마다 다른 것(안전 설정, 생각하기 수준의 이름, 종료 사유, 오류의 모양)은 이 패키지 안에서 끝난다.
// 부르는 쪽은 ai.Request를 주고 ai.Response나 ai의 실패 종류 하나를 받는다.
//
// 연결(Client)은 하나만 만들고, 일마다 모델을 골라 LLM을 꺼내 쓴다.
// 연결을 함께 써야 요청마다 새로 맺는 시간(0.1초 남짓)을 아낀다.
//
// 요청과 답의 내용은 로그에 남기지 않는다. 길이, 토큰 수, 모델, 걸린 시간, 실패 종류만 남긴다.
// SDK가 돌려준 오류에는 요청 본문이나 답의 일부가 섞여 있을 수 있어서, 그 오류를 감싸지도 않는다.
// 종류만 가려 ai.Error로 바꾼다.
package gemini

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"google.golang.org/genai"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/clock"
	"github.com/sirin-interact/tmrlife/server/internal/config"
)

// Backend는 같은 모델을 어느 창구로 부를지다.
type Backend string

const (
	// BackendDeveloperAPI는 API 키로 부르는 Gemini Developer API다.
	BackendDeveloperAPI Backend = "developer_api"
)

const (
	// DefaultMaxOutputTokens는 요청도 모델 설정도 한도를 정하지 않았을 때의 출력 한도다.
	//
	// 한도를 잡는 규칙: 한도 = 기대하는 가장 긴 답 + 생각 토큰의 몫.
	// 답에 보이지 않는 생각 토큰도 이 한도에서 빠진다. 생각하기 수준을 낮게 두어도 모델은 무거운 말을 들으면
	// 스스로 생각을 늘린다. 평소의 말에는 0개를 쓰다가 죽음을 말하는 발화에는 수백 개에서 2천 개 가까이 쓴다.
	// 한도를 두 문장짜리 답에 맞춰 400으로 두면 바로 그런 발화에서 답이 문장 중간에 끊긴다.
	// 가장 조심해야 할 순간에만 실패하는 셈이다.
	//
	// 한도는 상한일 뿐이고 비용은 실제로 만든 토큰에만 든다. 그래서 짧은 답을 받는 일에도 넉넉히 둔다.
	// 한도에 걸린 답은 ai.ErrTruncated로 돌려준다. 되풀이되면 한도를 올린다.
	DefaultMaxOutputTokens = 8192

	// OutputTokenFloor보다 작은 한도는 이 값으로 올려서 보낸다.
	//
	// 잘린 답은 어차피 실패로 버려지므로 낮은 한도로 얻는 것이 없다. 답의 길이는 지시문과 출력 검사로 다스린다.
	// 반대로 낮은 한도는 생각 토큰이 몰리는 발화에서만 조용히 실패를 만든다. 그 길을 막는다.
	// 관찰한 생각 토큰의 최댓값(2천 개 남짓)의 두 배로 잡았다.
	OutputTokenFloor = 4096

	// DefaultRequestTimeout은 부른 쪽이 기한을 주지 않았을 때도 호출이 끝없이 매달려 있지 않게 하는 상한이다.
	// 응답 시간은 대부분 2초 안쪽이지만 가끔 10초를 넘긴다. 실시간 호출은 부르는 쪽이 이보다 훨씬 짧은 기한을 준다.
	DefaultRequestTimeout = 60 * time.Second

	developerAPIBaseURL = "https://generativelanguage.googleapis.com/"
)

// Config는 연결 하나를 만드는 데 필요한 값이다.
type Config struct {
	// Backend를 비워 두면 Developer API다.
	//
	// 입력과 출력의 보관을 직접 통제해야 하면 Vertex AI로 옮겨야 한다. 그때는 Backend 값을 하나 더하고
	// sdkConfig에 갈래를 더한다(프로젝트, 위치, 자격 증명). 요청을 만들고 답을 읽는 부분은 두 창구가 같아서 그대로 쓴다.
	Backend Backend
	// APIKey는 Developer API의 키다.
	APIKey config.Secret
	// Clock은 걸린 시간을 재는 데 쓴다.
	Clock clock.Clock
	// Logger를 비워 두면 로그를 남기지 않는다.
	Logger *slog.Logger
	// RequestTimeout이 0이면 DefaultRequestTimeout이다. 부른 쪽의 기한이 더 이르면 그쪽을 따른다.
	RequestTimeout time.Duration
	// HTTPClient를 비워 두면 연결을 유지하는 전용 클라이언트를 만든다. 시험에서 가짜 전송 계층을 끼울 때 쓴다.
	HTTPClient *http.Client
	// BaseURL을 비워 두면 공급자의 공식 주소다.
	BaseURL string
}

// Client는 공급자와의 연결 하나다. 여러 고루틴에서 함께 써도 된다.
type Client struct {
	models  *genai.Models
	clock   clock.Clock
	logger  *slog.Logger
	timeout time.Duration
}

// New는 연결을 만든다. 네트워크에는 나가지 않는다. 키나 모델 이름이 틀렸는지는 첫 호출에서 드러난다.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Clock == nil {
		return nil, errors.New("gemini: clock is required")
	}
	if cfg.RequestTimeout < 0 {
		return nil, errors.New("gemini: request timeout must not be negative")
	}

	sdkCfg, err := sdkConfig(cfg)
	if err != nil {
		return nil, err
	}
	sdk, err := genai.NewClient(ctx, sdkCfg)
	if err != nil {
		// SDK의 오류 문구에는 설정 전체가 찍히고 그 안에 키가 들어 있다. 그래서 감싸지 않고 고정 문구만 돌려준다.
		return nil, errors.New("gemini: sdk client could not be created")
	}

	client := &Client{
		models:  sdk.Models,
		clock:   cfg.Clock,
		logger:  cfg.Logger,
		timeout: cfg.RequestTimeout,
	}
	if client.logger == nil {
		client.logger = slog.New(slog.DiscardHandler)
	}
	if client.timeout == 0 {
		client.timeout = DefaultRequestTimeout
	}
	return client, nil
}

func sdkConfig(cfg Config) (*genai.ClientConfig, error) {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = newHTTPClient()
	}

	switch cfg.Backend {
	case "", BackendDeveloperAPI:
		if !cfg.APIKey.IsSet() {
			return nil, errors.New("gemini: api key is required for the developer api backend")
		}
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = developerAPIBaseURL
		}
		return &genai.ClientConfig{
			// 창구와 주소를 여기서 못 박는다. 비워 두면 SDK가 환경 변수(GOOGLE_GENAI_USE_VERTEXAI, GOOGLE_GEMINI_BASE_URL)를 읽어
			// 정하는데, 그러면 설정에 없는 변수 하나로 사용자의 말이 다른 곳으로 나갈 수 있다.
			Backend:     genai.BackendGeminiAPI,
			APIKey:      cfg.APIKey.Reveal(),
			HTTPClient:  httpClient,
			HTTPOptions: genai.HTTPOptions{BaseURL: baseURL},
		}, nil
	default:
		return nil, errors.New("gemini: unsupported backend")
	}
}

// newHTTPClient는 모든 LLM이 함께 쓰는 클라이언트다.
// 전체 시간 제한을 두지 않는다. 기한은 호출마다 컨텍스트로 준다.
func newHTTPClient() *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{}
	}
	transport = transport.Clone()
	// 한 턴에 대화 모델과 판별 모델을 같은 주소로 동시에 부르고, 예비 요청까지 겹칠 수 있다.
	// HTTP/1.1로 떨어졌을 때도 그 연결들이 버려지지 않고 다음 턴에 다시 쓰이게 한다.
	transport.MaxIdleConnsPerHost = 8
	return &http.Client{Transport: transport}
}

// Model은 LLM 하나가 부를 모델과 그 기본값이다.
type Model struct {
	Name string
	// Thinking은 요청이 생각하기 수준을 정하지 않았을 때 쓰는 수준이다. 둘 다 비어 있으면 모델의 기본 수준이다.
	// 모델마다 받는 수준이 다르다. 받지 않는 수준을 보내면 공급자가 요청을 거절하고, 그 호출은 ai.ErrInvalidRequest로 끝난다.
	// 조용히 다른 수준으로 바꿔 보내지 않는다. 설정이 틀린 것을 숨기면 그 일 전체가 기대와 다른 속도와 품질로 돈다.
	Thinking string
	// PinThinking이 true면 요청이 수준을 정했더라도 Thinking을 쓴다.
	//
	// 예비 모델을 위한 것이다. 주 모델과 예비 모델은 같은 요청을 받으므로 요청에 적힌 수준도 같이 받는다.
	// 그런데 같은 수준이라도 모델에 따라 생각에 쓰는 양이 크게 다르다. 늦은 답을 대신하려고 띄우는 모델이
	// 주 모델의 수준을 물려받아 몇 초씩 생각하면 띄우는 뜻이 없다.
	PinThinking bool
	// MaxOutputTokens는 요청이 한도를 정하지 않았을 때 쓰는 한도다. 0이면 DefaultMaxOutputTokens다.
	MaxOutputTokens int
}

var modelNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,63}$`)

// LLM은 모델 하나를 부르는 ai.LLM이자 ai.StreamLLM이다. 여러 고루틴에서 함께 써도 된다.
type LLM struct {
	client          *Client
	model           string
	thinking        string
	pinThinking     bool
	maxOutputTokens int
}

var (
	_ ai.LLM       = (*LLM)(nil)
	_ ai.StreamLLM = (*LLM)(nil)
)

// LLM은 이 연결로 모델 하나를 부르는 LLM을 만든다.
func (c *Client) LLM(m Model) (*LLM, error) {
	// 모델 이름은 주소의 일부가 되고 오류와 로그에도 그대로 들어간다.
	if !modelNamePattern.MatchString(m.Name) {
		return nil, errors.New("gemini: model name is empty or has unexpected characters")
	}
	if m.Thinking != "" {
		if _, ok := thinkingLevels[m.Thinking]; !ok {
			return nil, errors.New("gemini: unknown thinking level")
		}
	}
	if m.PinThinking && m.Thinking == "" {
		return nil, errors.New("gemini: a pinned thinking level needs a level")
	}
	if m.MaxOutputTokens < 0 {
		return nil, errors.New("gemini: max output tokens must not be negative")
	}
	return &LLM{
		client:          c,
		model:           m.Name,
		thinking:        m.Thinking,
		pinThinking:     m.PinThinking,
		maxOutputTokens: m.MaxOutputTokens,
	}, nil
}

// Model은 이 LLM이 부르는 모델의 이름이다. ai.Response.Model에 찍히는 값과 같다.
func (l *LLM) Model() string { return l.model }

// Generate는 답 전체를 한 번에 받는다.
func (l *LLM) Generate(ctx context.Context, req ai.Request) (ai.Response, error) {
	call := l.begin(req, false)
	resp, err := l.generate(ctx, req, call)
	l.finish(ctx, call, resp, err)
	return resp, err
}

// GenerateStream은 답을 만들어지는 대로 onDelta에 넘긴다. 생각 토큰은 넘기지 않는다.
func (l *LLM) GenerateStream(ctx context.Context, req ai.Request, onDelta ai.DeltaFunc) (ai.Response, error) {
	call := l.begin(req, true)
	resp, err := l.generateStream(ctx, req, onDelta, call)
	l.finish(ctx, call, resp, err)
	return resp, err
}

// probeTask는 Probe가 보내는 요청의 지시문 ID다.
const probeTask = "provider-probe"

// Probe는 아주 짧은 요청 하나로 키, 모델 이름, 생각하기 수준이 받아들여지는지 확인한다.
//
// 이 셋이 틀리면 그 모델을 쓰는 일이 호출마다 실패한다. 위기 판별에서는 그 실패가 "규칙만으로 판정"으로 조용히 넘어가므로,
// 첫 사용자가 오기 전에 알아야 한다. ai.ErrInvalidRequest면 설정이 틀린 것이고, 다른 실패는 공급자 쪽의 일시적인 문제일 수 있다.
func (l *LLM) Probe(ctx context.Context) error {
	_, err := l.Generate(ctx, ai.Request{
		Task:     probeTask,
		System:   "Reply with the single word: ok",
		Messages: []ai.Message{{Role: ai.RoleUser, Text: "ping"}},
	})
	return err
}

func (l *LLM) generate(ctx context.Context, req ai.Request, call *callRecord) (ai.Response, error) {
	contents, cfg, err := l.prepare(ctx, req, call)
	if err != nil {
		return ai.Response{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, l.client.timeout)
	defer cancel()

	out, err := l.client.models.GenerateContent(ctx, l.model, contents, cfg)
	if err != nil {
		return ai.Response{}, classifyError(ctx, err, l.detail(req))
	}

	var acc accumulator
	acc.add(out)
	return l.conclude(req, &acc, call)
}

func (l *LLM) generateStream(ctx context.Context, req ai.Request, onDelta ai.DeltaFunc, call *callRecord) (ai.Response, error) {
	if onDelta == nil {
		return ai.Response{}, ai.NewError(ai.ErrInvalidRequest, ai.Detail{Task: l.detail(req).Task, Model: l.model, Reason: "nil_delta_func"})
	}
	contents, cfg, err := l.prepare(ctx, req, call)
	if err != nil {
		return ai.Response{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, l.client.timeout)
	defer cancel()

	var acc accumulator
	// 고리를 중간에 빠져나가면 SDK가 응답 본문을 닫는다.
	for chunk, err := range l.client.models.GenerateContentStream(ctx, l.model, contents, cfg) {
		if err != nil {
			return ai.Response{}, classifyError(ctx, err, l.detail(req))
		}
		delta := acc.add(chunk)
		if acc.blockReason != "" {
			break
		}
		if delta == "" {
			continue
		}
		if !call.sawDelta {
			call.sawDelta = true
			call.firstDelta = l.client.clock.Now().Sub(call.started)
		}
		if err := onDelta(delta); err != nil {
			// 받는 쪽이 그만 받겠다고 한 것이다. 그 오류에 무엇이 들었는지 모르므로 손대지 않고 그대로 돌려준다.
			return ai.Response{}, err
		}
	}
	// 기한이 지나 흐름이 끊기면 SDK는 오류 없이 고리를 끝내기도 한다. 그것을 "답이 끝났다"로 읽지 않는다.
	if err := ctx.Err(); err != nil {
		return ai.Response{}, ai.ContextError(err, l.detail(req))
	}
	return l.conclude(req, &acc, call)
}

// prepare는 보내기 전에 끝낼 수 있는 검사를 모두 하고 SDK에 넘길 값을 만든다.
func (l *LLM) prepare(ctx context.Context, req ai.Request, call *callRecord) ([]*genai.Content, *genai.GenerateContentConfig, error) {
	if err := req.Validate(); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, ai.ContextError(err, l.detail(req))
	}
	contents, cfg, err := l.buildRequest(req)
	if err != nil {
		return nil, nil, err
	}
	call.maxOutputTokens = int(cfg.MaxOutputTokens)
	if cfg.ThinkingConfig != nil {
		call.thinking = string(cfg.ThinkingConfig.ThinkingLevel)
	}
	return contents, cfg, nil
}

// conclude는 모은 답을 쓸 수 있는 답인지 가려서 돌려준다.
func (l *LLM) conclude(req ai.Request, acc *accumulator, call *callRecord) (ai.Response, error) {
	call.modelVersion = acc.modelVersion
	call.providerReason = acc.providerReason()
	return acc.result(req, l.model)
}

// detail은 실패에 붙일 설명의 바탕이다. 검증을 통과하지 못한 지시문 ID는 문장일 수도 있으므로 옮기지 않는다.
func (l *LLM) detail(req ai.Request) ai.Detail {
	task := req.Task
	if !ai.ValidTaskID(task) {
		task = ""
	}
	return ai.Detail{Task: task, Model: l.model}
}
