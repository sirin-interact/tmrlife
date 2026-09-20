// Package gate는 위기 관문의 두 겹을 한데 묶어 발화 하나에 대한 두 판정을 낸다.
//
// 첫째 겹(rules)은 정해 둔 표현을 찾는 규칙이고, 둘째 겹(classifier)은 대화를 만드는 모델과 분리된 AI 판별이다.
// 여기서는 두 겹을 돌려 core/crisis가 받는 꼴로 내놓는 데까지만 한다. 최종 단계는 crisis.Decide가 정한다.
// 그 사람의 최근 상태와 지난 판정은 이 패키지가 알지 못한다.
//
// 모든 사용자 발화는 답이 나가기 전에 이 관문을 거쳐야 한다. 부르는 쪽은 Detect가 돌아오기 전에 만든 답을 내보내지 않는다.
package gate

import (
	"context"
	"errors"
	"log/slog"

	"github.com/sirin-interact/tmrlife/server/internal/core/crisis"
	"github.com/sirin-interact/tmrlife/server/internal/gate/classifier"
	"github.com/sirin-interact/tmrlife/server/internal/gate/rules"
)

// Classifier는 둘째 겹이다. *classifier.Classifier가 이것을 채운다.
type Classifier interface {
	Classify(ctx context.Context, in classifier.Input) classifier.Result
}

// Detection은 발화 하나에 대한 두 겹의 판정이다. 사용자의 말(근거)이 들어 있으므로 통째로 로그에 넘기지 않는다.
// 넘기더라도 LogValue가 식별자, 단계, 걸린 시간만 남긴다.
type Detection struct {
	Rule rules.Result
	AI   classifier.Result
}

// Detector는 두 겹을 돌린다. 여러 고루틴에서 함께 써도 된다.
type Detector struct {
	lexicon    *rules.Lexicon
	classifier Classifier
}

// New는 관문을 만든다.
func New(lexicon *rules.Lexicon, cls Classifier) (*Detector, error) {
	if lexicon == nil {
		return nil, errors.New("gate: lexicon is required")
	}
	if cls == nil {
		return nil, errors.New("gate: classifier is required")
	}
	return &Detector{lexicon: lexicon, classifier: cls}, nil
}

// Detect는 발화 하나에 두 겹을 모두 돌린다. 규칙은 즉시 끝나고, AI 판별은 정해 둔 시간 안에서 기다린다.
//
// 규칙이 이미 가장 높은 단계를 냈어도 AI 판별을 건너뛰지 않는다. 두 겹의 판정이 갈린 기록이 있어야
// 사전이 지나치게 잡는 말과 놓치는 말을 나중에 가려낼 수 있다.
// 그 시간을 기다릴 수 없는 쪽은 Rule을 먼저 부르고 AI 판별을 따로 돌리면 된다.
func (d *Detector) Detect(ctx context.Context, in classifier.Input) Detection {
	return Detection{
		Rule: d.Rule(in.Utterance),
		AI:   d.classifier.Classify(ctx, in),
	}
}

// Rule은 첫째 겹만 돌린다. 모델을 부르지 않으므로 바로 끝난다.
func (d *Detector) Rule(utterance string) rules.Result {
	return d.lexicon.Scan(utterance)
}

// Input은 crisis.Input의 두 겹 자리를 채운다. 나머지(상태, 직접 물었는지, 지난 판정, 시각)는 부르는 쪽이 채운다.
func (d Detection) Input() crisis.Input {
	return crisis.Input{Rule: d.Rule.Core(), AI: d.AI.Core()}
}

// Evidence는 판정 기록에 남길 근거 발화를 고른다. 어느 겹도 잡지 않았으면 빈 글이다.
//
// 더 높은 단계를 낸 겹의 근거를 쓴다. 단계가 같으면 AI 판별의 근거를 쓴다.
// 규칙의 근거는 패턴이 걸린 자리의 문장이고, AI 판별의 근거는 뜻을 읽고 고른 말이라 나중에 읽는 사람에게 더 쓸모 있다.
// 고른 겹의 근거가 비어 있으면 다른 겹의 근거를, 그것도 없으면 빈 글을 돌려준다.
func (d Detection) Evidence() string {
	ruleStage, aiStage := d.Rule.Stage, crisis.StageNone
	if d.AI.Answered {
		aiStage = d.AI.Stage
	}
	if ruleStage < crisis.StageCheck && aiStage < crisis.StageCheck {
		return ""
	}

	first, second := d.AI.Evidence, d.Rule.Evidence
	if ruleStage > aiStage {
		first, second = second, first
	}
	if first != "" {
		return first
	}
	return second
}

// LogValue는 판정을 통째로 로그에 넘겨도 사용자의 말이 나가지 않게 한다.
func (d Detection) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("rule", d.Rule),
		slog.Any("ai", d.AI),
	)
}
