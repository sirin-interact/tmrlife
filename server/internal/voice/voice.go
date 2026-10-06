// Package voice는 음성 인식과 음성 합성을 부르는 쪽과 공급자 사이의 약속이다.
//
// 대화 엔진은 글을 받아 글을 낸다. 이 패키지는 그 앞뒤에 붙는 소리의 길을 정한다.
// 소리는 어디에도 저장하지 않는다. 구현은 소리를 공급자에 흘려보내고 글이나 소리를 돌려줄 뿐 파일로 쓰지 않는다.
//
// 오류에는 사용자의 말을 넣지 않는다. 인식된 글도 오류 문구에 섞지 않는다.
package voice

import (
	"context"
	"errors"
	"io"
)

// 올라가는 소리와 내려가는 소리의 꼴. 바꾸면 웹앱의 캡처 워클릿과 재생 워클릿도 함께 바꿔야 한다.
const (
	// InputSampleRate는 브라우저가 서버로 올려 보내는 소리의 샘플레이트다. PCM s16le 모노.
	InputSampleRate = 16000
	// InputBytesPerSecond는 실시간으로 올라오는 소리의 초당 바이트 수다. 한도를 셀 때 쓴다.
	InputBytesPerSecond = InputSampleRate * 2
	// OutputSampleRate는 서버가 브라우저로 내려보내는 소리의 샘플레이트다. PCM s16le 모노.
	// 합성기 구현은 이 값으로 소리를 돌려줘야 한다(Synthesizer.SampleRate).
	OutputSampleRate = 24000
)

var (
	// ErrUnavailable은 공급자를 쓸 수 없다는 뜻이다(키 없음, 꺼 둠). 부르는 쪽은 채팅으로 내려간다.
	ErrUnavailable = errors.New("voice: provider is unavailable")
	// ErrStreamClosed는 이미 닫힌 인식 스트림에 소리를 넣었다는 뜻이다.
	ErrStreamClosed = errors.New("voice: recognition stream is closed")
)

// Recognizer는 연결마다 인식 스트림 하나를 연다.
type Recognizer interface {
	// Open은 스트림을 연다. 공급자와의 연결이 맺어진 뒤에 돌아온다. ctx는 여는 동안에만 쓴다.
	Open(ctx context.Context) (RecognitionStream, error)
}

// RecognitionStream은 소리를 받아 글을 내는 스트림 하나다.
//
// 구현이 지킬 것:
//   - Write와 Finalize는 Events를 읽는 고루틴과 다른 고루틴에서 불린다. 함께 불러도 안전해야 한다.
//   - Events는 사건을 난 순서대로 넘긴다. 스트림이 끝나면(Close, 또는 더 이어갈 수 없는 실패) 채널을 닫는다.
//     실패로 끝날 때는 닫기 전에 EventFailed를 하나 넘긴다.
//   - 소리가 한동안 오지 않아도 공급자와의 연결을 스스로 지킨다(keepalive).
//   - Close는 몇 번을 불러도 되고, 그 뒤의 Write는 ErrStreamClosed다.
//   - 소리도 글도 로그와 오류에 넣지 않는다.
type RecognitionStream interface {
	// Write는 PCM s16le 16kHz 모노 소리 한 조각을 넣는다. 조각의 크기는 자유다.
	Write(ctx context.Context, pcm []byte) error
	// Finalize는 지금까지 들은 말을 끝점을 기다리지 않고 바로 확정하게 한다.
	Finalize(ctx context.Context) error
	Events() <-chan Event
	Close() error
}

// EventKind는 인식 사건의 종류다.
type EventKind int

const (
	// EventPartial은 아직 확정되지 않은 글이다. 자막에만 쓴다.
	// Text는 지금 말하고 있는 한 마디 전체다(확정된 앞부분 + 아직 바뀔 수 있는 뒷부분).
	EventPartial EventKind = iota + 1
	// EventFinal은 끝점까지의 한 마디다. 이 글로 턴이 돈다.
	EventFinal
	// EventFailed는 스트림이 더 이어지지 않는다는 뜻이다. 이 사건 뒤에 채널이 닫힌다.
	EventFailed
)

// Event는 인식 스트림이 낸 사건 하나다.
type Event struct {
	Kind EventKind
	// Text는 Partial과 Final의 글이다. 앞뒤 공백은 뗀다. Final의 글이 비어 있으면 부르는 쪽이 버린다.
	Text string
	// MinConfidence는 Final에서 낱말 토큰(한글, 숫자, 로마자가 든 토큰) 가운데 가장 낮은 확신도(0~1)다.
	// 쉼표 같은 부호 토큰은 세지 않는다. 낱말 토큰이 없으면 1이다.
	MinConfidence float32
	// Err는 Failed의 까닭이다.
	Err error
}

// Synthesizer는 글 하나를 소리로 바꾼다.
//
// 구현이 지킬 것:
//   - 돌려주는 소리는 PCM s16le 모노이고 샘플레이트는 SampleRate()다(OutputSampleRate와 같아야 한다).
//   - 소리는 만들어지는 대로 읽힌다. 첫 바이트가 나오기 전에 전체를 기다리지 않는다.
//   - ctx가 끝나면 읽기가 오류로 끝나고 공급자 호출이 멈춘다. 끼어들기가 이 길로 온다.
//   - 글을 로그와 오류에 넣지 않는다.
type Synthesizer interface {
	// Synthesize는 소리 스트림을 돌려준다. 부르는 쪽이 끝까지 읽거나 Close해야 한다.
	Synthesize(ctx context.Context, text string) (io.ReadCloser, error)
	SampleRate() int
}

// Provider는 두 공급자를 한데 묶은 것이다. 채널은 이것 하나를 받는다.
type Provider struct {
	Recognizer  Recognizer
	Synthesizer Synthesizer
}

// Available은 둘 다 있는지다. 하나라도 없으면 음성 방식을 열지 않는다.
func (p Provider) Available() bool {
	return p.Recognizer != nil && p.Synthesizer != nil
}
