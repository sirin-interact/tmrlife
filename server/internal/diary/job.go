package diary

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
	"github.com/sirin-interact/tmrlife/server/internal/store/db"
)

// DraftArgs는 일기 초안 작업의 인자다. 식별자만 담는다.
// 인자는 작업 큐의 테이블에 평문으로 남으므로 글이나 날짜 같은 사용자의 기록을 넣지 않는다.
type DraftArgs struct {
	UserID uuid.UUID `json:"user_id"`
	// DayID는 초안을 만들 하루다. 작업은 이 하루의 끝난 대화 가운데 아직 담기지 않은 것을 모두 다룬다.
	DayID uuid.UUID `json:"day_id"`
	// ConversationID는 이 작업을 등록하게 만든, 방금 끝난 대화다. 로그에서 작업과 대화를 잇는 데만 쓴다.
	ConversationID uuid.UUID `json:"conversation_id"`
}

// Kind는 작업 종류의 이름이다. DB에 저장되므로 한번 정하면 바꾸지 않는다.
func (DraftArgs) Kind() string { return "diary_draft" }

// ArgsFor는 방금 끝난 대화에서 작업 인자를 만든다.
func ArgsFor(conversation db.Conversation) DraftArgs {
	return DraftArgs{
		UserID:         conversation.UserID,
		DayID:          conversation.DayID,
		ConversationID: conversation.ID,
	}
}

// Enqueuer는 일기 초안 작업을 등록한다.
type Enqueuer struct {
	inserter    *queue.Client
	maxAttempts int
}

// NewEnqueuer의 inserter는 작업을 넣기만 하는 큐 클라이언트(queue.NewInsertClient)면 된다.
// maxAttempts는 처음 시도까지 더한 전체 시도 횟수다.
// 시도 횟수는 작업을 넣을 때 정해진다. 작업자 쪽에서는 바꿀 수 없어서 넣는 쪽이 설정을 들고 있다.
func NewEnqueuer(inserter *queue.Client, maxAttempts int) (*Enqueuer, error) {
	if inserter == nil {
		return nil, errors.New("diary: queue client is required")
	}
	if maxAttempts < 1 {
		return nil, errors.New("diary: max attempts must be at least 1")
	}
	return &Enqueuer{inserter: inserter, maxAttempts: maxAttempts}, nil
}

// EnqueueTx는 대화를 끝내는 트랜잭션 안에서 초안 작업을 등록한다.
// 같은 트랜잭션이어야 한다. 따로 넣으면 대화는 끝났는데 작업이 없거나, 작업자가 아직 끝나지 않은 대화를 보게 된다.
//
// 대화를 끝낸 쪽(EndConversation이 행을 돌려준 쪽)만 부른다. 위기 대응이 있었던 대화에도 불러도 된다.
// 작업이 그 대화를 재료에서 빼고 진행 상태만 닫는다.
func (e *Enqueuer) EnqueueTx(ctx context.Context, tx pgx.Tx, args DraftArgs) error {
	if tx == nil {
		return errors.New("diary: enqueue needs a transaction")
	}
	if args.UserID == uuid.Nil || args.DayID == uuid.Nil || args.ConversationID == uuid.Nil {
		return errors.New("diary: job args need user, day and conversation ids")
	}
	if _, err := e.inserter.InsertTx(ctx, tx, args, &river.InsertOpts{MaxAttempts: e.maxAttempts}); err != nil {
		return fmt.Errorf("diary: enqueue draft job: %w", err)
	}
	return nil
}

// Worker는 일기 초안 작업을 실행한다.
type Worker struct {
	river.WorkerDefaults[DraftArgs]
	service *Service
	logger  *slog.Logger
	timeout time.Duration
}

func NewWorker(service *Service, logger *slog.Logger) (*Worker, error) {
	if service == nil {
		return nil, errors.New("diary: worker needs a service")
	}
	if logger == nil {
		return nil, errors.New("diary: worker needs a logger")
	}
	return &Worker{service: service, logger: logger, timeout: service.MaxDuration()}, nil
}

// Timeout은 작업 하나의 제한 시간이다. 모델을 기다리는 시간보다 짧으면 느린 답을 받기도 전에 작업이 끊긴다.
func (w *Worker) Timeout(*river.Job[DraftArgs]) time.Duration { return w.timeout }

// NextRetry는 실패한 시도 뒤에 다음 시도를 언제 할지 정한다. 큐의 기본 간격은 초안을 기다리는 사용자에게 너무 길다.
//
// 시각은 주입받은 시계에서 온다. 큐는 제 시계로 그 시각을 본다. 가짜 시계를 쓰는 실행에서 두 시계가 어긋나 있으면
// 큐가 지난 시각은 제 기본 간격으로 바꾸고, 앞선 시각은 그만큼 기다린다. 그런 실행은 큐를 거치지 않고 Service를 직접 부른다.
func (w *Worker) NextRetry(job *river.Job[DraftArgs]) time.Time {
	return w.service.NextRetry(job.Attempt)
}

// Work는 초안을 만든다. 실패하면 오류를 돌려줘 큐가 다시 시도하게 하고, 마지막 시도였거나 다시 해도 소용없는 실패면 포기 절차를 밟는다.
// 포기까지 마쳤으면 작업은 성공으로 끝난다. 할 일을 다 했기 때문이다.
func (w *Worker) Work(ctx context.Context, job *river.Job[DraftArgs]) error {
	return w.work(ctx, job.Args, attempt{jobID: job.ID, number: job.Attempt, max: job.MaxAttempts})
}

// attempt는 큐가 매긴 이번 시도의 번호다.
type attempt struct {
	jobID  int64
	number int
	max    int
}

func (w *Worker) work(ctx context.Context, args DraftArgs, try attempt) error {
	target := Target{UserID: args.UserID, DayID: args.DayID}
	attrs := []slog.Attr{
		slog.Int64("job_id", try.jobID),
		slog.Int("attempt", try.number),
		slog.Int("max_attempts", try.max),
		slog.String("user_id", args.UserID.String()),
		slog.String("day_id", args.DayID.String()),
		slog.String("conversation_id", args.ConversationID.String()),
	}

	result, err := w.service.Draft(ctx, target)
	if err == nil {
		w.logger.LogAttrs(ctx, slog.LevelInfo, "diary draft job finished", append(attrs, slog.Any("result", result))...)
		return nil
	}

	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		// 작업자가 내려가는 중이다. 모델이 실패한 것이 아니므로 시도 횟수를 쓰지 않고 다음 작업자가 바로 집게 한다.
		w.logger.LogAttrs(context.WithoutCancel(ctx), slog.LevelInfo, "diary draft job interrupted", attrs...)
		return river.JobSnooze(0)
	}

	// 오류의 문구는 남기지 않는다. 정해 둔 이름만 남긴다.
	//
	// 다시 해 볼 실패는 이 함수가 돌려주는 오류가 작업 큐의 행(river_job.errors)에 그대로 적힌다.
	// 그 행은 일기가 암호문으로 누워 있는 바로 그 데이터베이스에 평문으로 남는다.
	// 지금 여기로 오는 오류는 모두 정해진 문구만 담고 있지만, 그 약속을 이 자리에서 지키게 한다.
	attrs = append(attrs,
		slog.String("failure", failureName(err)),
		slog.Any("result", result),
	)
	lastAttempt := try.number >= try.max
	if !lastAttempt && !Permanent(err) {
		w.logger.LogAttrs(ctx, slog.LevelWarn, "diary draft job failed, will retry", attrs...)
		return tokenError{name: failureName(err), err: err}
	}

	gaveUp, giveUpErr := w.service.GiveUp(ctx, target)
	if giveUpErr != nil {
		w.logger.LogAttrs(ctx, slog.LevelError, "diary draft job could not give up cleanly",
			append(attrs, slog.String("give_up_failure", failureName(giveUpErr)))...)
		return tokenError{name: failureName(err), err: errors.Join(err, giveUpErr)}
	}
	w.logger.LogAttrs(ctx, slog.LevelWarn, "diary draft job gave up",
		append(attrs, slog.String("give_up_outcome", string(gaveUp.Outcome)))...)
	return nil
}

// tokenError는 감싼 오류를 errors.Is로 그대로 가릴 수 있게 두되, 글로는 정해 둔 이름만 내놓는다.
//
// 다시 해 볼 실패는 Work가 돌려주는 오류가 작업 큐의 행에 그대로 적힌다. 그 행은 일기가 암호문으로 누워 있는
// 바로 그 데이터베이스에 평문으로 남는다. 지금 여기로 오는 오류는 모두 정해진 문구만 담고 있지만,
// 그 약속을 다음에 오류 길이 하나 늘어나는 날에도 지키려면 마지막 자리에서 한 번 걸러야 한다.
type tokenError struct {
	name string
	err  error
}

func (e tokenError) Error() string { return "diary: draft failed: " + e.name }
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
	}
	if kind := ai.Classify(err).Kind; kind != ai.KindUnknown && kind != ai.KindNone {
		return "ai_" + string(kind)
	}
	return "other"
}
