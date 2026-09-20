// Package testdb는 시험마다 깨끗한 PostgreSQL 데이터베이스를 하나씩 내준다.
//
// 가짜 DB로는 외래 키, 트랜잭션, citext 같은 실제 동작을 확인할 수 없어서 진짜 PostgreSQL을 컨테이너로 띄운다.
// 컨테이너는 시험 프로세스마다 한 번만 띄우고 마이그레이션도 한 번만 돌린다.
// 시험 하나하나는 그 결과를 틀(template)로 복제한 자기만의 데이터베이스를 받으므로
// 서로의 데이터를 보지 못하고, t.Parallel()로 나란히 돌려도 된다.
package testdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/sirin-interact/tmrlife/server/internal/dbmigrate"
	"github.com/sirin-interact/tmrlife/server/internal/pgdb"
)

const (
	// docker-compose.yml의 로컬 DB와 같은 버전을 쓴다.
	image        = "postgres:18"
	templateName = "naeil_template"
	user         = "naeil"
	password     = "naeil"
	// pg_stat_activity에서 시험이 연 연결을 가려낼 수 있게 한다.
	appName = "naeil-test"

	startTimeout = 2 * time.Minute
	stepTimeout  = 30 * time.Second

	createAttempts   = 20
	createRetryDelay = 100 * time.Millisecond

	dockerCLITimeout = 5 * time.Second
)

type server struct {
	// adminURL은 데이터베이스를 만들고 지우는 데 쓰는 접속 주소다.
	adminURL string
}

var (
	startOnce sync.Once
	shared    *server
	errStart  error
	counter   atomic.Int64
	createMu  sync.Mutex

	dockerHostOnce sync.Once
)

// New는 모든 마이그레이션이 적용된 빈 데이터베이스와 그 접속 풀을 돌려준다.
// 시험이 끝나면 풀을 닫고 데이터베이스를 지운다.
//
// Docker가 없으면 로컬에서는 시험을 건너뛴다. CI에서는 건너뛰지 않고 실패시킨다.
// 조용히 건너뛰면 DB 시험이 한 번도 돌지 않은 채로 통과한 것처럼 보이기 때문이다.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return create(t, templateName)
}

// NewEmpty는 마이그레이션을 하나도 적용하지 않은 데이터베이스를 돌려준다.
// 마이그레이션 자체를 시험할 때 쓴다.
func NewEmpty(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return create(t, "template0")
}

func create(t *testing.T, template string) *pgxpool.Pool {
	t.Helper()

	dockerHostOnce.Do(useDockerCLIContext)
	if os.Getenv("CI") == "" {
		testcontainers.SkipIfProviderIsNotHealthy(t)
	}

	startOnce.Do(func() { shared, errStart = start() })
	if errStart != nil {
		t.Fatalf("testdb: start postgres container: %v", errStart)
	}

	ctx, cancel := context.WithTimeout(context.Background(), stepTimeout)
	defer cancel()

	name := fmt.Sprintf("t_%d_%d", os.Getpid(), counter.Add(1))

	if err := shared.createFromTemplate(ctx, name, template); err != nil {
		t.Fatalf("testdb: create database: %v", err)
	}

	// 운영과 같은 방식으로 연다. 시험이 보는 시간대, 시각의 꼴, 연결 한도가 운영과 같아야
	// 시험에서 통과한 코드가 운영에서도 같은 값을 본다.
	pool, err := pgdb.Open(ctx, shared.urlFor(name), appName)
	if err != nil {
		t.Fatalf("testdb: connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("testdb: ping: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		// t의 컨텍스트는 Cleanup 시점에 이미 끝나 있어 새로 만든다.
		dropCtx, dropCancel := context.WithTimeout(context.Background(), stepTimeout)
		defer dropCancel()
		if err := shared.exec(dropCtx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, name)); err != nil {
			t.Logf("testdb: drop database %s: %v", name, err)
		}
	})
	return pool
}

// useDockerCLIContext는 docker 명령이 쓰고 있는 컨텍스트의 소켓을 testcontainers에도 알려준다.
//
// testcontainers는 DOCKER_HOST와 기본 소켓(/var/run/docker.sock)만 보고, docker 명령의 컨텍스트 설정은 읽지 않는다.
// 그래서 OrbStack이나 Colima처럼 소켓이 다른 자리에 있으면 docker는 잘 되는데 시험만 "Docker 없음"으로 건너뛰게 된다.
// DOCKER_HOST를 직접 준 경우에는 그 값을 존중해 아무것도 하지 않는다.
func useDockerCLIContext() {
	if os.Getenv("DOCKER_HOST") != "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), dockerCLITimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}").Output()
	if err != nil {
		return
	}
	host := strings.TrimSpace(string(out))
	if host == "" || host == "unix:///var/run/docker.sock" {
		return
	}
	// 시험 프로세스 안에서만 바뀐다. testcontainers가 환경 변수로만 받기 때문에 이 길밖에 없다.
	_ = os.Setenv("DOCKER_HOST", host)
}

// start는 컨테이너를 띄우고 마이그레이션이 적용된 틀 데이터베이스를 만든다.
// 컨테이너는 따로 내리지 않는다. testcontainers의 정리 컨테이너가 시험 프로세스가 끝나면 지운다.
func start() (*server, error) {
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	ctr, err := postgres.Run(ctx, image,
		postgres.WithDatabase("postgres"),
		postgres.WithUsername(user),
		postgres.WithPassword(password),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, fmt.Errorf("run container: %w", err)
	}

	adminURL, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("connection string: %w", err)
	}
	s := &server{adminURL: adminURL}

	if err := s.exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, templateName)); err != nil {
		return nil, fmt.Errorf("create template database: %w", err)
	}

	// 틀을 복제하려면 틀에 붙어 있는 연결이 하나도 없어야 한다. 마이그레이션이 끝나면 풀을 바로 닫는다.
	pool, err := pgdb.Open(ctx, s.urlFor(templateName), appName)
	if err != nil {
		return nil, fmt.Errorf("connect to template database: %w", err)
	}
	defer pool.Close()

	logger := slog.New(slog.DiscardHandler)
	if _, err := dbmigrate.Up(ctx, pool, logger); err != nil {
		return nil, fmt.Errorf("migrate template database: %w", err)
	}
	return s, nil
}

// createFromTemplate은 틀을 복제해 새 데이터베이스를 만든다.
func (s *server) createFromTemplate(ctx context.Context, name, template string) error {
	// 같은 틀을 동시에 복제하면 PostgreSQL이 거부할 수 있어 만드는 순간만 줄을 세운다.
	createMu.Lock()
	defer createMu.Unlock()

	stmt := fmt.Sprintf(`CREATE DATABASE %q TEMPLATE %q`, name, template)
	var err error
	for range createAttempts {
		err = s.exec(ctx, stmt)
		// 55006(object_in_use): 방금 닫은 연결의 서버 쪽 프로세스가 아직 틀에서 빠져나가는 중이다. 잠깐 뒤에 풀린다.
		var pgErr *pgconn.PgError
		if err == nil || !errors.As(err, &pgErr) || pgErr.Code != "55006" {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(createRetryDelay):
		}
	}
	return err
}

func (s *server) exec(ctx context.Context, sql string) error {
	pool, err := pgdb.Open(ctx, s.adminURL, appName)
	if err != nil {
		return err
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, sql)
	return err
}

func (s *server) urlFor(database string) string {
	u, err := url.Parse(s.adminURL)
	if err != nil {
		return s.adminURL
	}
	u.Path = "/" + database
	return u.String()
}
