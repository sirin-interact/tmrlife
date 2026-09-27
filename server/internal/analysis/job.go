package analysis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/sirin-interact/tmrlife/server/internal/ai"
	"github.com/sirin-interact/tmrlife/server/internal/queue"
	"github.com/sirin-interact/tmrlife/server/internal/store"
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// ExtractArgs는 신호 추출 작업의 인자다. 식별자만 담는다.
// 인자는 작업 큐의 테이블에 평문으로 남으므로 발화나 날짜 같은 사용자의 기록을 넣지 않는다.
type ExtractArgs struct {
	UserID uuid.UUID `json:"user_id"`
	// ConversationID는 신호를 뽑을 대화다. 작업은 이 대화 하나만 다룬다.
	ConversationID uuid.UUID `json:"conversation_id"`
}

// Kind는 작업 종류의 이름이다. DB에 저장되므로 한번 정하면 바꾸지 않는다.
func (ExtractArgs) Kind() string { return "signal_extract" }

// ArgsFor는 방금 끝난 대화에서 작업 인자를 만든다.
func ArgsFor(conversation db.Conversation) ExtractArgs {
	return ExtractArgs{UserID: conversation.UserID, ConversationID: conversation.ID}
}

// target은 작업 인자를 Service가 받는 꼴로 바꾼다. 두 타입의 필드가 어긋나면 이 변환에서 컴파일이 깨진다.
func (a ExtractArgs) target() Target {
	return Target(a)
}

// Enqueuer는 신호 추출 작업을 등록한다.
type Enqueuer struct {
	inserter    *queue.Client
	maxAttempts int
}

// NewEnqueuer의 inserter는 작업을 넣기만 하는 큐 클라이언트(queue.NewInsertClient)면 된다.
// maxAttempts는 처음 시도까지 더한 전체 시도 횟수다.
// 시도 횟수는 작업을 넣을 때 정해진다. 작업자 쪽에서는 바꿀 수 없어서 넣는 쪽이 설정을 들고 있다.
func NewEnqueuer(inserter *queue.Client, maxAttempts int) (*Enqueuer, error) {
	if inserter == nil {
		return nil, errors.New("analysis: queue client is required")
	}
	if maxAttempts < 1 {
		return nil, errors.New("analysis: max attempts must be at least 1")
	}
	return &Enqueuer{inserter: inserter, maxAttempts: maxAttempts}, nil
}

// EnqueueTx는 대화를 끝내는 트랜잭션 안에서 추출 작업을 등록하고, 그 대화의 분석 상태를 pending으로 적는다.
//
// 같은 트랜잭션이어야 한다. 따로 넣으면 대화는 끝났는데 작업이 없거나, 작업자가 아직 끝나지 않은 대화를 보게 된다.
// 상태와 작업을 함께 적으므로, pending인 대화에는 언제나 작업이 있고 작업이 있는 대화는 언제나 pending에서 시작한다.
//
// q는 그 트랜잭션의 쿼리(store.InTxRaw가 넘겨준 것)여야 한다. 대화를 끝낸 뒤에 부른다.
// 위기 대응이 있었던 대화에도 부른다. 신호는 그런 대화에서도 뽑는다.
func (e *Enqueuer) EnqueueTx(ctx context.Context, tx pgx.Tx, q *db.Queries, args ExtractArgs) error {
	if tx == nil || q == nil {
		return errors.New("analysis: enqueue needs a transaction")
	}
	if args.UserID == uuid.Nil || args.ConversationID == uuid.Nil {
		return errors.New("analysis: job args need user and conversation ids")
	}
	if _, err := q.SetConversationAnalysisStatus(ctx, db.SetConversationAnalysisStatusParams{
		AnalysisStatus: store.AnalysisPending, ID: args.ConversationID, UserID: args.UserID,
	}); err != nil {
		return fmt.Errorf("analysis: mark conversation pending: %w", err)
	}
	if _, err := e.inserter.InsertTx(ctx, tx, args, &river.InsertOpts{MaxAttempts: e.maxAttempts}); err != nil {
		return fmt.Errorf("analysis: enqueue extract job: %w", err)
	}
	return nil
}

// Worker는 신호 추출 작업을 실행한다.
type Worker struct {
	river.WorkerDefaults[ExtractArgs]
	service *Service
	logger  *slog.Logger
	timeout time.Duration
}

func NewWorker(service *Service, logger *slog.Logger) (*Worker, error) {
	if service == nil {
		return nil, errors.New("analysis: worker needs a service")
	}
	if logger == nil {
		return nil, errors.New("analysis: worker needs a logger")
	}
	return &Worker{service: service, logger: logger, timeout: service.MaxDuration()}, nil
}

// Timeout은 작업 하나의 제한 시간이다. 모델을 기다리는 시간보다 짧으면 느린 답을 받기도 전에 작업이 끊긴다.
func (w *Worker) Timeout(*river.Job[ExtractArgs]) time.Duration { return w.timeout }

// NextRetry는 실패한 시도 뒤에 다음 시도를 언제 할지 정한다.
//
// 시각은 주입받은 시계에서 온다. 큐는 제 시계로 그 시각을 본다. 가짜 시계를 쓰는 실행에서 두 시계가 어긋나 있으면
// 큐가 지난 시각은 제 기본 간격으로 바꾸고, 앞선 시각은 그만큼 기다린다. 그런 실행은 큐를 거치지 않고 Service를 직접 부른다.
func (w *Worker) NextRetry(job *river.Job[ExtractArgs]) time.Time {
	return w.service.NextRetry(job.Attempt)
}

// Work는 신호를 뽑는다. 실패하면 오류를 돌려줘 큐가 다시 시도하게 하고,
// 마지막 시도였거나 다시 해도 소용없는 실패면 분석을 failed로 닫는다. 닫기까지 마쳤으면 작업은 성공으로 끝난다.
func (w *Worker) Work(ctx context.Context, job *river.Job[ExtractArgs]) error {
	return w.work(ctx, job.Args, attempt{jobID: job.ID, number: job.Attempt, max: job.MaxAttempts})
}

// attempt는 큐가 매긴 이번 시도의 번호다.
type attempt struct {
	jobID  int64
	number int
	max    int
}

func (w *Worker) work(ctx context.Context, args ExtractArgs, try attempt) error {
	target := args.target()
	attrs := []slog.Attr{
		slog.Int64("job_id", try.jobID),
		slog.Int("attempt", try.number),
		slog.Int("max_attempts", try.max),
		slog.String("user_id", args.UserID.String()),
		slog.String("conversation_id", args.ConversationID.String()),
	}

	result, err := w.service.Extract(ctx, target)
	if err == nil {
		w.logger.LogAttrs(ctx, slog.LevelInfo, "signal extraction job finished", append(attrs, slog.Any("result", result))...)
		return nil
	}

	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		// 작업자가 내려가는 중이다. 모델이 실패한 것이 아니므로 시도 횟수를 쓰지 않고 다음 작업자가 바로 집게 한다.
		w.logger.LogAttrs(context.WithoutCancel(ctx), slog.LevelInfo, "signal extraction job interrupted", attrs...)
		return river.JobSnooze(0)
	}

	// 오류의 문구는 남기지 않는다. 정해 둔 이름만 남긴다.
	//
	// 다시 해 볼 실패는 이 함수가 돌려주는 오류가 작업 큐의 행(river_job.errors)에 그대로 적힌다.
	// 그 행은 발화와 근거가 암호문으로 누워 있는 바로 그 데이터베이스에 평문으로 남는다.
	// 지금 여기로 오는 오류는 모두 정해진 문구만 담고 있지만, 그 약속을 이 자리에서 지키게 한다.
	attrs = append(attrs,
		slog.String("failure", failureName(err)),
		slog.Any("result", result),
	)
	lastAttempt := try.number >= try.max
	if !lastAttempt && !Permanent(err) {
		w.logger.LogAttrs(ctx, slog.LevelWarn, "signal extraction job failed, will retry", attrs...)
		return tokenError{name: failureName(err), err: err}
	}

	gaveUp, giveUpErr := w.service.GiveUp(ctx, target)
	if giveUpErr != nil {
		w.logger.LogAttrs(ctx, slog.LevelError, "signal extraction job could not give up cleanly",
			append(attrs, slog.String("give_up_failure", failureName(giveUpErr)))...)
		return tokenError{name: failureName(err), err: errors.Join(err, giveUpErr)}
	}
	w.logger.LogAttrs(ctx, slog.LevelWarn, "signal extraction job gave up",
		append(attrs, slog.String("give_up_outcome", string(gaveUp.Outcome)))...)
	return nil
}

// tokenError는 감싼 오류를 errors.Is로 그대로 가릴 수 있게 두되, 글로는 정해 둔 이름만 내놓는다.
//
// 다시 해 볼 실패는 Work가 돌려주는 오류가 작업 큐의 행에 그대로 적힌다. 그 행은 발화가 암호문으로 누워 있는
// 바로 그 데이터베이스에 평문으로 남는다. 오류 길이 하나 늘어나는 날에도 그 약속이 지켜지도록 마지막 자리에서 한 번 걸러낸다.
type tokenError struct {
	name string
	err  error
}

func (e tokenError) Error() string { return "analysis: signal extraction failed: " + e.name }
func (e tokenError) Unwrap() error { return e.err }

// failureName은 실패를 로그와 지표에 쓸 고정된 이름으로 바꾼다.
func failureName(err error) string {
	var rejected *RejectError
	switch {
	case errors.As(err, &rejected):
		return "rejected_" + rejected.Reason
	case errors.Is(err, ErrUnreadable):
		return "unreadable"
	case errors.Is(err, ErrBusy):
		return "busy"
	case errors.Is(err, ErrStillActive):
		return "still_active"
	}
	if kind := ai.Classify(err).Kind; kind != ai.KindUnknown && kind != ai.KindNone {
		return "ai_" + string(kind)
	}
	return "other"
}
