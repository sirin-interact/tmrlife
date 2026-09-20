// Package pgdb는 PostgreSQL 접속 풀을 여는 단 하나의 자리다.
//
// 서버, 작업자, 마이그레이션, 시험이 모두 여기서 풀을 연다. 여는 자리가 둘이면 한쪽에만 한도가 걸리거나
// 한쪽만 다른 시간대로 시각을 돌려주는 식으로 어긋나고, 그 차이는 운영에서야 드러난다.
package pgdb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// pgx의 기본 한도는 CPU 개수를 따른다. 컨테이너에서는 노드 전체의 CPU가 보여서,
	// 큰 노드에 파드 몇 개만 떠도 DB의 연결 한도를 다 써 버린다. 그래서 프로세스 하나가 쓸 몫을 고정한다.
	defaultMaxConns = 10

	// 연결을 주기적으로 새로 맺어야 DB의 주 서버가 바뀐 뒤에 옛 서버에 붙은 연결이 남지 않는다.
	// 모든 연결이 같은 순간에 끊기지 않도록 수명을 조금씩 흩어 놓는다.
	defaultMaxConnLifetime       = 30 * time.Minute
	defaultMaxConnLifetimeJitter = 5 * time.Minute
	defaultMaxConnIdleTime       = 10 * time.Minute

	defaultConnectTimeout = 5 * time.Second
)

// errUnparsableURL은 접속 주소를 읽지 못했다는 오류다. 드라이버의 오류를 감싸지 않고 이 고정된 문구만 돌려준다.
//
// 드라이버의 오류에는 접속 주소가 통째로 들어 있다. 비밀번호는 가려 준다지만 가리다 마는 꼴이 있고
// (키=값 꼴에서 비밀번호에 공백이나 어긋난 따옴표가 있으면 뒤쪽 조각이 그대로 남는다), 호스트와 사용자, DB 이름은 늘 그대로 나온다.
// 이 오류는 서버가 뜨다 실패할 때 로그에 찍히므로, 값은 한 글자도 싣지 않고 어느 변수를 고쳐야 하는지만 알려준다.
var errUnparsableURL = errors.New(
	"parse database url: DATABASE_URL is not a valid connection string " +
		"(the value is not shown; check the port, sslmode and pool_* options)")

// Open은 한도를 정한 접속 풀을 만든다.
//
// 실제 연결은 처음 쓸 때 맺는다. DB가 잠깐 내려가 있어도 여기서는 실패하지 않으므로,
// DB 없이는 뜰 수 없는 프로세스는 돌려받은 풀에 직접 Ping을 해 본다.
//
// 한도는 접속 주소에 적혀 있으면(pool_max_conns 등) 그 값을 따르고, 없을 때만 여기의 기본값을 쓴다.
// 환경마다 다른 값은 코드가 아니라 접속 주소로 조정한다.
//
// appName은 pg_stat_activity에 남는 프로세스 이름이다. 어느 실행 파일의 연결인지 가려야 하므로 비워 둘 수 없고,
// 접속 주소에 적힌 application_name보다 우선한다.
func Open(ctx context.Context, databaseURL, appName string) (*pgxpool.Pool, error) {
	if appName == "" {
		return nil, errors.New("open database pool: application name is required")
	}

	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errUnparsableURL
	}
	// 풀 설정은 읽고 나면 지워지므로, 주소에 무엇이 적혀 있었는지는 한 번 더 읽어서 안다.
	given, err := pgconn.ParseConfig(databaseURL)
	if err != nil {
		return nil, errUnparsableURL
	}

	if _, ok := given.RuntimeParams["pool_max_conns"]; !ok {
		cfg.MaxConns = defaultMaxConns
	}
	if _, ok := given.RuntimeParams["pool_max_conn_lifetime"]; !ok {
		cfg.MaxConnLifetime = defaultMaxConnLifetime
	}
	if _, ok := given.RuntimeParams["pool_max_conn_lifetime_jitter"]; !ok {
		cfg.MaxConnLifetimeJitter = defaultMaxConnLifetimeJitter
	}
	if _, ok := given.RuntimeParams["pool_max_conn_idle_time"]; !ok {
		cfg.MaxConnIdleTime = defaultMaxConnIdleTime
	}
	if cfg.ConnConfig.ConnectTimeout == 0 {
		cfg.ConnConfig.ConnectTimeout = defaultConnectTimeout
	}

	cfg.ConnConfig.RuntimeParams["application_name"] = appName
	// 세션 시간대를 고정한다. 기록 날짜의 경계는 애플리케이션이 사용자 시간대로 계산하므로,
	// DB 서버의 시간대 설정에 따라 date나 timestamptz의 변환 결과가 달라지는 일이 없어야 한다.
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"

	// 타입 설정은 연결마다 따로 들고 있다. 풀이 연결을 새로 맺을 때마다 다시 걸어야 한다.
	cfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		scanTimestampsInUTC(conn.TypeMap())
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	return pool, nil
}

// scanTimestampsInUTC는 읽어 온 timestamptz에 UTC를 붙여 돌려주게 한다.
//
// 드라이버는 기본으로 프로세스의 시간대(time.Local)를 붙인다. 가리키는 순간은 같지만,
// 개발 장비(Asia/Seoul)와 배포 환경(UTC)에서 같은 코드가 다른 날짜와 시각을 찍게 된다.
// 주입받은 시계도 UTC로 맞춘 값을 돌려주므로, DB에 넣었다 꺼낸 값과 시계에서 받은 값의 꼴이 같아진다.
// 사용자의 시간대가 필요한 계산은 그 시간대를 명시해서 바꿔 쓴다.
func scanTimestampsInUTC(types *pgtype.Map) {
	timestamptz := &pgtype.Type{
		Name:  "timestamptz",
		OID:   pgtype.TimestamptzOID,
		Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
	}
	types.RegisterType(timestamptz)
	// 배열은 원소의 타입을 따로 들고 있다. 함께 바꾸지 않으면 배열로 읽은 시각만 옛 방식으로 돌아온다.
	types.RegisterType(&pgtype.Type{
		Name:  "_timestamptz",
		OID:   pgtype.TimestamptzArrayOID,
		Codec: &pgtype.ArrayCodec{ElementType: timestamptz},
	})
}
