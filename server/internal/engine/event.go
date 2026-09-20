package engine

import (
	"context"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/sirin-interact/tmrlife/server/internal/phrases"
	"github.com/sirin-interact/tmrlife/server/internal/recorddate"
)

// Event는 엔진이 내보내는 사건 하나다. 종류는 아래 다섯이고, 받는 쪽은 타입 스위치로 가린다.
//
// 글이 든 사건은 통째로 로그에 넘겨도 내용이 나가지 않도록 LogValue를 가진다.
type Event interface {
	// isEvent는 이 패키지 밖에서 새 종류를 만들지 못하게 막는다. 받는 쪽의 타입 스위치가 빠짐없이 다뤄야 하기 때문이다.
	isEvent()
}

// Ready는 대화가 열렸다는 사건이다. 연결마다 가장 먼저 한 번 나간다.
type Ready struct {
	ConversationID uuid.UUID
	RecordDate     recorddate.Date
	// Resumed는 열려 있던 대화를 이어가는 것인지다. 거짓이면 새 대화이고 곧이어 첫 안부가 나간다.
	Resumed bool
	// Utterances는 이어가는 대화의 지난 말이다. 순번대로이고, 새 대화면 비어 있다.
	Utterances []Utterance
	// ResourcesPinned는 이 대화에서 도움 자원을 화면에 고정해 두었는지다. 참이면 곧이어 Resources가 나간다.
	ResourcesPinned bool
}

// Accepted는 사용자의 글을 저장했고 답을 준비하기 시작했다는 사건이다. 받았다는 확인이기도 하다.
type Accepted struct {
	ClientMessageID uuid.UUID
	Seq             int32
}

// AIText는 사용자에게 나가는 말 하나다. 관문의 판정이 저장된 뒤에만 나온다.
type AIText struct {
	Seq int32
	// Text는 화면에 보이고 발화로 저장된 글이다.
	Text string
	// Speech는 음성 합성에 넘기는 글이다. 미리 써 둔 말은 숫자가 한글로 풀려 있다.
	Speech string
	// Origin은 store.OriginModel, store.OriginFixed, store.OriginTemplate 가운데 하나다.
	Origin string
	// Phrase는 미리 써 둔 말이 나갔을 때 그 문구의 이름이다.
	Phrase phrases.ID
}

// Resources는 화면에 고정할 도움 자원이다. 한 연결에서 한 번만 나간다.
type Resources struct {
	// Items는 급한 곳이 앞에 오는 순서일 수 있다. 받은 순서 그대로 보여준다.
	Items []phrases.Resource
}

// Ended는 대화가 끝났다는 사건이다.
type Ended struct {
	// Reason은 store.EndReason*이다.
	Reason     string
	RecordDate recorddate.Date
	// DiaryExpected가 참이면 일기 초안 작업이 등록되었다. 거짓이면 초안은 만들어지지 않는다.
	DiaryExpected bool
}

// Utterance는 이어가는 대화에 돌려주는 지난 말 하나다.
type Utterance struct {
	Seq int32
	// Speaker는 store.SpeakerUser나 store.SpeakerAI다.
	Speaker string
	// Origin은 store.Origin*이다.
	Origin string
	Text   string
	// ClientMessageID는 사용자의 글이면 보낼 때 붙인 식별자이고, AI의 말이면 nil이다.
	ClientMessageID *uuid.UUID
	CreatedAt       time.Time
}

func (Ready) isEvent()     {}
func (Accepted) isEvent()  {}
func (AIText) isEvent()    {}
func (Resources) isEvent() {}
func (Ended) isEvent()     {}

// LogValue는 지난 말의 글 대신 수만 남긴다.
func (e Ready) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("event", "ready"),
		slog.String("conversation_id", e.ConversationID.String()),
		slog.String("record_date", e.RecordDate.String()),
		slog.Bool("resumed", e.Resumed),
		slog.Int("utterances", len(e.Utterances)),
		slog.Bool("resources_pinned", e.ResourcesPinned),
	)
}

func (e Accepted) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("event", "accepted"),
		slog.String("client_message_id", e.ClientMessageID.String()),
		slog.Int("seq", int(e.Seq)),
	)
}

// LogValue는 말의 글 대신 출처와 글자 수만 남긴다.
func (e AIText) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("event", "ai_text"),
		slog.Int("seq", int(e.Seq)),
		slog.String("origin", e.Origin),
		slog.String("phrase", string(e.Phrase)),
		slog.Int("chars", utf8.RuneCountInString(e.Text)),
	)
}

func (e Resources) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("event", "resources"),
		slog.Int("items", len(e.Items)),
	)
}

func (e Ended) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("event", "ended"),
		slog.String("reason", e.Reason),
		slog.String("record_date", e.RecordDate.String()),
		slog.Bool("diary_expected", e.DiaryExpected),
	)
}

// LogValue는 지난 말의 글 대신 순번과 글자 수만 남긴다.
func (u Utterance) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("seq", int(u.Seq)),
		slog.String("speaker", u.Speaker),
		slog.String("origin", u.Origin),
		slog.Int("chars", utf8.RuneCountInString(u.Text)),
	)
}

// Sink는 엔진이 낸 사건을 받는 쪽이다. 채팅 채널, 음성 파이프라인, 시험이 저마다 구현한다.
//
// Emit은 사건이 난 순서대로, 한 번에 하나씩 불린다. 오류를 돌려주면 그 턴은 거기서 멈춘다.
// 이미 저장된 것은 되돌리지 않는다. 연결이 끊겨 보내지 못한 말도 대화에는 남아 있어야 이어갈 때 보인다.
type Sink interface {
	Emit(ctx context.Context, e Event) error
}

// SinkFunc는 함수 하나로 Sink를 만든다.
type SinkFunc func(ctx context.Context, e Event) error

func (f SinkFunc) Emit(ctx context.Context, e Event) error { return f(ctx, e) }
