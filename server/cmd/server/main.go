// Command server는 REST와 WebSocket 요청을 받는 API 서버다.
//
//	server [serve]                 서버를 띄운다 (기본)
//	server migrate up              앱 스키마와 작업 큐 스키마를 끝까지 올린다
//	server migrate down            앱 스키마를 한 단계 내린다 (MIGRATE_ALLOW_DOWN=true일 때만, prod에서는 하지 않는다)
//	server migrate status          적용 상태를 보여준다
//	server seed demo               시연용 계정과 지난 며칠치 기록을 만든다 (prod에서는 하지 않는다)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	// 시간대 자료를 실행 파일 안에 넣는다. 가입할 때 받은 시간대를 확인하고 기록 날짜의 경계를 그 시간대로 계산하는데,
	// 운영체제에 시간대 파일이 없는 곳에서는 이 자료가 없으면 time.LoadLocation이 실패한다.
	// 빌드 명령의 태그(-tags timetzdata)에만 기대면 다른 방법으로 빌드한 실행 파일에서 조용히 빠진다.
	// 코드에서 가져오면 어떻게 빌드해도 들어간다.
	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sirin-interact/tmrlife/server/internal/app"
	"github.com/sirin-interact/tmrlife/server/internal/config"
	"github.com/sirin-interact/tmrlife/server/internal/dbmigrate"
	"github.com/sirin-interact/tmrlife/server/internal/httpserver"
	"github.com/sirin-interact/tmrlife/server/internal/logging"
	"github.com/sirin-interact/tmrlife/server/internal/pgdb"
)

const (
	exitOK      = 0
	exitFailure = 1
	// exitUsage는 명령이나 설정이 틀려서 시작조차 못 한 경우다. 다시 띄워도 고쳐지지 않는다.
	exitUsage = 2

	startupPingTimeout = 5 * time.Second
	migrateTimeout     = 10 * time.Minute
)

const usage = `usage:
  server [serve]
  server migrate up|down|status
  server seed demo --email=EMAIL --password=PASSWORD [--days=N] [--name=NAME]
`

func main() {
	// 종료 신호를 컨텍스트 취소로 바꾼다. 서버는 이 취소를 보고 처리 중인 요청을 마친 뒤 내려간다.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// 첫 신호를 받으면 신호를 가로채는 일을 그만둔다. 요청이 끝나기를 기다리는 동안 한 번 더 누르면 바로 내려간다.
	// 그대로 두면 두 번째 신호는 아무 일도 하지 않아서, 급히 내려야 할 때 강제 종료 말고는 방법이 없다.
	go func() {
		<-ctx.Done()
		stop()
	}()
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	command := "serve"
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}

	switch command {
	case "serve":
		if len(args) != 0 {
			_, _ = fmt.Fprint(stderr, usage)
			return exitUsage
		}
		return serve(ctx, stdout, stderr)
	case "migrate":
		if len(args) != 1 {
			_, _ = fmt.Fprint(stderr, usage)
			return exitUsage
		}
		return migrate(ctx, args[0], stdout, stderr)
	case "seed":
		if len(args) == 0 || args[0] != "demo" {
			_, _ = fmt.Fprint(stderr, usage)
			return exitUsage
		}
		return seedDemo(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return exitOK
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n%s", command, usage)
		return exitUsage
	}
}

func serve(ctx context.Context, stdout, stderr io.Writer) int {
	cfg, err := config.Load()
	if err != nil {
		// 로거를 만들기 전이다. 설정 오류에는 변수 이름과 이유만 있고 값은 없다.
		_, _ = fmt.Fprintln(stderr, err)
		return exitUsage
	}

	logger := logging.New(stdout, cfg.LogLevel).With(slog.String("app", app.NameServer))
	logger.LogAttrs(ctx, slog.LevelInfo, "starting", slog.Any("config", cfg))

	// 마스터 키나 지시문 파일이 틀렸으면 여기서 끝난다. 첫 요청이 와서야 알게 되는 일이 없게 한다.
	deps, err := app.New(ctx, cfg, logger, app.NameServer)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "build dependencies", slog.String("error", err.Error()))
		return exitFailure
	}
	defer deps.Close()
	logger.LogAttrs(ctx, slog.LevelInfo, "dependencies ready", slog.Any("prompts", deps.Prompts.Tasks()))

	// 경로가 기대는 것(인증 서비스, 언어 모델, 지시문, 명세, 시도 한도)이 틀렸으면 첫 요청이 아니라 여기서 드러난다.
	handler, err := deps.NewHTTPHandler(ctx)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "build http handler", slog.String("error", err.Error()))
		return exitFailure
	}

	// DB가 아직 안 떴다고 서버를 죽이지는 않는다. 죽고 다시 뜨기를 되풀이해 봐야 나아지는 것이 없고,
	// 풀은 DB가 돌아오면 알아서 다시 붙는다. 그동안은 /readyz가 503으로 트래픽을 막는다.
	pingCtx, cancelPing := context.WithTimeout(ctx, startupPingTimeout)
	if err := deps.Pool.Ping(pingCtx); err != nil {
		logger.LogAttrs(ctx, slog.LevelWarn, "database is not reachable yet", slog.String("error", err.Error()))
	}
	cancelPing()

	srv := httpserver.NewServer(httpserver.ServerOptions{
		Addr:    cfg.HTTPAddr,
		Handler: handler.Echo,
		Logger:  logger,
		// 넘겨받은 연결(대화 소켓)은 Shutdown이 닫아 주지도, 기다려 주지도 않는다. 내려가기 시작할 때 직접 닫고 기다린다.
		OnDrain: handler.CloseSockets,
	})
	if err := srv.Run(ctx); err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "server stopped with error", slog.String("error", err.Error()))
		return exitFailure
	}
	return exitOK
}

func migrate(ctx context.Context, action string, stdout, stderr io.Writer) int {
	switch action {
	case "up", "down", "status":
	default:
		_, _ = fmt.Fprintf(stderr, "unknown migrate action %q\n%s", action, usage)
		return exitUsage
	}

	cfg, err := config.LoadMigration()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitUsage
	}

	if action == "down" {
		if reason := refuseDown(cfg); reason != "" {
			// DB에 붙기 전에 끝낸다. 거부한 명령이 연결 하나라도 열 이유가 없다.
			_, _ = fmt.Fprintln(stderr, reason)
			return exitUsage
		}
	}

	// 상태 표는 stdout으로, 진행 로그는 stderr로 보낸다. 표를 다른 명령에 넘겨도 로그가 섞이지 않는다.
	logger := logging.New(stderr, cfg.LogLevel).With(slog.String("app", app.NameMigrate))

	ctx, cancel := context.WithTimeout(ctx, migrateTimeout)
	defer cancel()

	// 스키마만 고치는 일에는 마스터 키도 지시문도 필요 없다. 그래서 다른 것은 만들지 않고 풀만 연다.
	pool, err := pgdb.Open(ctx, cfg.DatabaseURL.Reveal(), app.NameMigrate)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "open database pool", slog.String("error", err.Error()))
		return exitFailure
	}
	defer pool.Close()

	if err := runMigration(ctx, action, pool, logger, stdout); err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "migration failed",
			slog.String("action", action),
			slog.String("error", err.Error()),
		)
		return exitFailure
	}
	return exitOK
}

// refuseDown은 스키마를 내리면 안 되는 까닭을 돌려준다. 내려도 되면 빈 문자열이다.
//
// 내리는 구문은 테이블을 통째로 지운다. 가장 최근 마이그레이션이 기록 테이블을 만든 것이라면 명령 한 번에 모든 일기가 사라진다.
// 운영 이미지에 들어 있는 명령 가운데 데이터를 지우는 것은 이것 하나뿐이라서, 손이 미끄러져도 돌지 않게 한다.
// 운영에서는 스키마를 내리지 않는다. 되돌릴 일이 있으면 앞선 이미지로 다시 배포하고, 스키마는 앞으로만 고친다.
func refuseDown(cfg config.Migration) string {
	switch {
	case cfg.Env.IsProd():
		return "migrate down is refused when APP_ENV=prod: it drops tables and their data. " +
			"Roll back by redeploying the previous image, and fix the schema with a new migration."
	case !cfg.AllowDown:
		return "migrate down drops tables and their data, so it only runs when MIGRATE_ALLOW_DOWN=true is set " +
			"(never when APP_ENV=prod). For local development use: make migrate-down"
	}
	return ""
}

func runMigration(ctx context.Context, action string, pool *pgxpool.Pool, logger *slog.Logger, stdout io.Writer) error {
	switch action {
	case "up":
		applied, err := dbmigrate.Up(ctx, pool, logger)
		if err != nil {
			return err
		}
		logger.LogAttrs(ctx, slog.LevelInfo, "migrations applied",
			slog.Any("app_versions", applied.App),
			slog.Any("queue_versions", applied.Queue),
		)
		return nil

	case "down":
		version, err := dbmigrate.Down(ctx, pool, logger)
		if err != nil {
			return err
		}
		if version == 0 {
			logger.LogAttrs(ctx, slog.LevelInfo, "nothing to roll back")
			return nil
		}
		logger.LogAttrs(ctx, slog.LevelInfo, "migration rolled back", slog.Int64("app_version", version))
		return nil

	case "status":
		status, err := dbmigrate.GetStatus(ctx, pool, logger)
		if err != nil {
			return err
		}
		return printStatus(stdout, status)

	default:
		return errors.New("unknown migrate action")
	}
}

func printStatus(w io.Writer, status dbmigrate.Status) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SCHEMA\tVERSION\tSTATE\tSOURCE")
	for _, a := range status.App {
		state := "pending"
		if a.Applied {
			state = "applied"
		}
		_, _ = fmt.Fprintf(tw, "app\t%d\t%s\t%s\n", a.Version, state, a.Name)
	}
	queueState := "applied"
	if status.QueueApplied < status.QueueLatest {
		queueState = "pending"
	}
	_, _ = fmt.Fprintf(tw, "queue\t%d/%d\t%s\t(river)\n", status.QueueApplied, status.QueueLatest, queueState)
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write status: %w", err)
	}
	return nil
}
