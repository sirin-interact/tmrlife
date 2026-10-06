package config

import (
	"strconv"
	"strings"
	"time"
)

// VoiceProvider는 음성 인식과 합성을 어디서 받을지다.
type VoiceProvider string

const (
	// VoiceProviderSonioxElevenLabs는 Soniox로 알아듣고 ElevenLabs로 읽는다. 두 키가 모두 있어야 한다.
	VoiceProviderSonioxElevenLabs VoiceProvider = "soniox_elevenlabs"
	// VoiceProviderFake는 대본대로 알아듣고 무음을 돌려준다. 브라우저 흐름 테스트와 키 없는 시험에 쓴다.
	VoiceProviderFake VoiceProvider = "fake"
	// VoiceProviderOff는 음성 방식을 열지 않는다. 대화는 채팅으로만 된다.
	VoiceProviderOff VoiceProvider = "off"
)

// Voice는 음성 방식의 설정이다. 공급자의 모델과 목소리, 말 끝을 기다리는 시간처럼 조정할 값은 코드가 아니라 여기에 둔다.
type Voice struct {
	// Provider를 설정에 적지 않았을 때: 두 키가 모두 있으면 soniox_elevenlabs, 아니면 off다. 운영에서는 fake를 받지 않는다.
	Provider VoiceProvider

	// SonioxURL은 실시간 인식의 WebSocket 주소다.
	SonioxURL string
	// SonioxModel은 실시간 인식 모델이다.
	SonioxModel string
	// LanguageHints는 인식기에 주는 언어 힌트다.
	LanguageHints []string
	// MaxEndpointDelay는 말이 멈춘 뒤 끝점을 내기까지 인식기가 기다리는 시간의 상한이다.
	// 기운 없는 사람은 말 중간에 자주 멈춘다. 일반 음성 서비스보다 길게 둔다.
	MaxEndpointDelay time.Duration
	// MishearBelow는 이 값보다 낮은 확신도로 알아들은 무거운 말(확인 단계)에 되묻는 기준이다. 0이면 되묻지 않는다.
	MishearBelow float32

	// ElevenLabsURL은 합성 API의 주소다.
	ElevenLabsURL string
	// VoiceID는 ElevenLabs의 목소리다. 하나로 고정해 매일 같은 상대와 이야기하는 느낌을 준다.
	VoiceID string
	// TTSModel은 합성 모델이다.
	TTSModel string

	// BargeIn이 참이면 AI가 말하는 동안 사용자가 말을 시작할 때 재생을 끊는다.
	// 스피커 소리가 마이크로 되돌아오는 기기에서는 끌 수 있다.
	BargeIn bool
}

const (
	// 인식기가 받는 끝점 대기의 범위다. 밖의 값은 공급자가 거절한다.
	minEndpointDelay = 500 * time.Millisecond
	maxEndpointDelay = 3 * time.Second
)

// loadVoice는 음성 설정을 읽는다. 공급자를 고르는 규칙은 Voice.Provider의 설명과 같다.
func loadVoice(r raw, prod, sonioxKeySet, elevenLabsKeySet bool) (Voice, []Problem) {
	var problems []Problem
	add := func(name, reason string) {
		problems = append(problems, Problem{Var: name, Reason: reason})
	}

	v := Voice{
		SonioxURL:     strings.TrimSpace(r.SonioxURL),
		SonioxModel:   strings.TrimSpace(r.SonioxModel),
		ElevenLabsURL: strings.TrimSpace(r.ElevenLabsURL),
		VoiceID:       strings.TrimSpace(r.ElevenLabsVoiceID),
		TTSModel:      strings.TrimSpace(r.ElevenLabsModel),
	}

	provider := VoiceProvider(strings.ToLower(strings.TrimSpace(r.VoiceProvider)))
	if provider == "" {
		provider = VoiceProviderOff
		if sonioxKeySet && elevenLabsKeySet {
			provider = VoiceProviderSonioxElevenLabs
		}
	}
	switch provider {
	case VoiceProviderSonioxElevenLabs:
		if !sonioxKeySet {
			add("SONIOX_API_KEY", "is required when VOICE_PROVIDER=soniox_elevenlabs")
		}
		if !elevenLabsKeySet {
			add("ELEVENLABS_API_KEY", "is required when VOICE_PROVIDER=soniox_elevenlabs")
		}
		for _, m := range []struct{ name, value string }{
			{"SONIOX_URL", v.SonioxURL},
			{"SONIOX_MODEL", v.SonioxModel},
			{"ELEVENLABS_URL", v.ElevenLabsURL},
			{"ELEVENLABS_VOICE_ID", v.VoiceID},
			{"ELEVENLABS_MODEL", v.TTSModel},
		} {
			if m.value == "" {
				add(m.name, "must not be empty")
			}
		}
		for _, u := range []struct{ name, value, scheme string }{
			{"SONIOX_URL", v.SonioxURL, "wss://"},
			{"ELEVENLABS_URL", v.ElevenLabsURL, "https://"},
		} {
			if u.value != "" && !strings.HasPrefix(u.value, u.scheme) && !strings.HasPrefix(u.value, strings.TrimSuffix(u.scheme, "s://")+"://") {
				add(u.name, "must start with "+u.scheme+" (or the plain scheme for a local test server)")
			}
		}
	case VoiceProviderFake:
		if prod {
			// 대본대로 알아듣는 가짜가 운영에 뜨면 사용자의 말이 아닌 글이 관문을 지나간다.
			add("VOICE_PROVIDER", "must not be fake when APP_ENV=prod")
		}
	case VoiceProviderOff:
	default:
		add("VOICE_PROVIDER", "must be one of soniox_elevenlabs, fake, off")
	}
	v.Provider = provider

	hints := strings.Split(r.STTLanguageHints, ",")
	for _, h := range hints {
		if h = strings.TrimSpace(h); h != "" {
			v.LanguageHints = append(v.LanguageHints, h)
		}
	}

	delay, err := time.ParseDuration(strings.TrimSpace(r.STTMaxEndpointDelay))
	switch {
	case err != nil:
		add("STT_MAX_ENDPOINT_DELAY", "must be a duration such as 3s or 1500ms")
	case delay < minEndpointDelay || delay > maxEndpointDelay:
		add("STT_MAX_ENDPOINT_DELAY", "must be between "+minEndpointDelay.String()+" and "+maxEndpointDelay.String())
	default:
		v.MaxEndpointDelay = delay
	}

	below, err := strconv.ParseFloat(strings.TrimSpace(r.STTMishearBelow), 32)
	switch {
	case err != nil, below < 0, below > 1:
		add("STT_MISHEAR_BELOW", "must be a number between 0 and 1 (0 turns the check off)")
	default:
		v.MishearBelow = float32(below)
	}

	bargeIn, err := strconv.ParseBool(strings.TrimSpace(r.VoiceBargeIn))
	if err != nil {
		add("VOICE_BARGE_IN", "must be true or false")
	} else {
		v.BargeIn = bargeIn
	}

	return v, problems
}
