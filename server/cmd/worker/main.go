// Command worker는 대화가 끝난 뒤의 일(일기, 신호 추출, 기억, 알림)을 큐에서 꺼내 실행하는 작업자다.
// 서버와 같은 DB를 보고, 따로 떠서 따로 늘리고 줄인다.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	// 시간대 자료를 실행 파일 안에 넣는다. 기록 날짜의 경계는 사용자의 시간대로 계산하는데,
	// 운영체제에 시간대 파일이 없는 곳에서는 이 자료가 없으면 time.LoadLocation이 실패한다.
	// 빌드 명령의 태그(-tags timetzdata)에만 기대면 다른 방법으로 빌드한 실행 파일에서 조용히 빠진다.
	// 코드에서 가져오면 어떻게 빌드해도 들어간다.
	_ "time/tzdata"

	"github.com/sirin-interact/tmrlife/server/internal/app"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
	"github.com/sirin-interact/tmrlife/server/internal/queue"
)

const (
	exitOK      = 0
	exitFailure = 1
	// exitUsage는 설정이 틀려서 시작조차 못 한 경우다. 다시 띄워도 고쳐지지 않는다.
	exitUsage = 2
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := run(ctx, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, stdout, stderr io.Writer) int {
	cfg, err := config.Load()
	// 상태 확인용 주소는 작업자만 쓰는 값이라 여기서 읽는다. 틀린 설정을 한 번에 모두 알려주려고 함께 확인한다.
	healthAddress, healthErr := healthAddr(os.Getenv(healthAddrEnv))
	if err != nil || healthErr != nil {
		// 로거를 만들기 전이다. 설정 오류에는 변수 이름과 이유만 있고 값은 없다.
		for _, problem := range []error{err, healthErr} {
			if problem != nil {
				_, _ = fmt.Fprintln(stderr, problem)
			}
		}
		return exitUsage
	}

	logger := logging.New(stdout, cfg.LogLevel).With(slog.String("app", app.NameWorker))
	logger.LogAttrs(ctx, slog.LevelInfo, "starting", slog.Any("config", cfg))

	deps, err := app.New(ctx, cfg, logger, app.NameWorker)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "build dependencies", slog.String("error", err.Error()))
		return exitFailure
	}
	defer deps.Close()

	// 분석에 쓰는 언어 모델이 없으면 일기 초안 작업이 등록되지 않는다. 모델을 만들지 못하면 뜨지 않는다.
	models, err := deps.NewModels(ctx)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "build language models", slog.String("error", err.Error()))
		return exitFailure
	}

	// 어떤 작업을 맡는지는 app이 모아 준다. 끝난 세션 지우기, 일기 초안, 버려진 대화 닫기다.
	options, err := deps.WorkerOptions(ctx, app.WithAnalysisModel(models.Analysis))
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "collect workers", slog.String("error", err.Error()))
		return exitFailure
	}
	client, err := queue.NewWorkerClient(deps.Pool, logger, options)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "create queue client", slog.String("error", err.Error()))
		return exitFailure
	}

	// 큐를 시작하기 전에 연다. 포트를 열지 못하는 설정이라면 작업을 하나도 집기 전에 드러난다.
	// 큐가 돌기 전까지는 "아직 아니다"로 답하므로, 배포는 새 작업자가 큐에 붙은 뒤에만 옛 작업자를 내린다.
	health, err := listenHealth(ctx, healthAddress, logger)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "open health listener", slog.String("error", err.Error()))
		return exitFailure
	}
	defer health.Close(ctx)

	// 서버와 달리 DB 없이는 할 수 있는 일이 없다. 닿지 않으면 바로 끝내고 다시 뜨게 둔다.
	// 큐 테이블이 없을 때도 여기서 걸린다. 마이그레이션(server migrate up)을 먼저 돌려야 한다.
	if err := client.Start(ctx); err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "start queue client", slog.String("error", err.Error()))
		return exitFailure
	}
	health.SetRunning(true)
	logger.LogAttrs(ctx, slog.LevelInfo, "worker started")

	// 종료 신호가 오면 ctx가 취소된다. 큐는 새 작업을 받지 않고, 돌던 작업을 정해진 시간만큼 기다린 뒤
	// 남은 작업의 컨텍스트를 취소한다. 끝내지 못한 작업은 큐가 나중에 다시 시도한다.
	<-client.Stopped()
	health.SetRunning(false)
	logger.LogAttrs(context.WithoutCancel(ctx), slog.LevelInfo, "worker stopped")
	return exitOK
}
