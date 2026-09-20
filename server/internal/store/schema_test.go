package store_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/dbmigrate"
	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

var appTables = []string{
	"users", "user_identities", "user_keys", "sessions", "consents", "user_settings",
	"days", "conversations", "utterances", "gate_events", "diaries", "signals",
	"memories", "mood_picks", "self_checks",
}

// 앱의 테이블만 고른다. 마이그레이션 기록과 작업 큐의 테이블은 각자의 도구가 관리한다.
const appTableFilter = `table_schema = 'public' AND table_name <> 'goose_db_version' AND table_name NOT LIKE 'river%'`

func existingTables(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT table_name::text FROM information_schema.tables WHERE `+appTableFilter+` ORDER BY table_name`)
	require.NoError(t, err)
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return names
}

func queryStrings(t *testing.T, pool *pgxpool.Pool, sql string) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), sql)
	require.NoError(t, err)
	values, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	return values
}

func TestMigrations(t *testing.T) {
	t.Parallel()

	t.Run("올리고, 끝까지 내리고, 다시 올려도 깨끗하다", func(t *testing.T) {
		t.Parallel()
		pool := testdb.NewEmpty(t)
		ctx := t.Context()
		logger := slog.New(slog.DiscardHandler)

		_, err := dbmigrate.Up(ctx, pool, logger)
		require.NoError(t, err)
		assert.ElementsMatch(t, appTables, existingTables(t, pool))

		for {
			version, err := dbmigrate.Down(ctx, pool, logger)
			require.NoError(t, err)
			if version == 0 {
				break
			}
		}
		assert.Empty(t, existingTables(t, pool), "내린 뒤에 남은 테이블이 있으면 Down 구문이 빠진 것이다")

		_, err = dbmigrate.Up(ctx, pool, logger)
		require.NoError(t, err)
		assert.ElementsMatch(t, appTables, existingTables(t, pool))
	})
}

// DB의 시계를 읽는 함수와 키워드다.
var dbClockPattern = regexp.MustCompile(
	`(?i)\b(now|clock_timestamp|statement_timestamp|transaction_timestamp|timeofday)\s*\(` +
		`|\b(current_timestamp|current_date|current_time|localtimestamp|localtime)\b`)

func TestQueriesDoNotReadTheDatabaseClock(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join("queries", "*.sql"))
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			body, err := os.ReadFile(file)
			require.NoError(t, err)
			for i, line := range strings.Split(string(body), "\n") {
				// 주석에서 규칙을 설명하느라 함수 이름을 적는 것은 괜찮다.
				code, _, _ := strings.Cut(line, "--")
				assert.NotRegexp(t, dbClockPattern, code, "%s:%d: 시각은 인자로 받는다", file, i+1)
			}
		})
	}

	t.Run("검사식이 실제로 걸러낸다", func(t *testing.T) {
		for _, sql := range []string{
			"SET updated_at = now()", "WHERE expires_at < NOW ()", "VALUES (CURRENT_TIMESTAMP)", "SELECT clock_timestamp()",
		} {
			assert.Regexp(t, dbClockPattern, sql)
		}
		for _, sql := range []string{"SET updated_at = sqlc.arg(now)", "WHERE known = true", "SELECT snow_depth"} {
			assert.NotRegexp(t, dbClockPattern, sql)
		}
	})
}

// plaintextColumns는 값을 평문으로 담아도 된다고 정한 컬럼이다. 식별자(uuid), 시각(timestamptz), 암호문(bytea)이 아닌 컬럼은
// 여기에 있어야 스키마에 들어올 수 있다.
//
// 사용자의 글은 암호화하지만 글에서 나온 판단 값은 DB가 규칙을 지켜 주도록 평문으로 둔다. 그 경계는 한 번 정하면 끝이 아니라
// 컬럼을 더할 때마다 다시 정해야 한다. 목록이 없으면 "기분 점수"나 "검진을 권한 까닭" 같은 값이 아무도 따져 보지 않은 채
// 평문으로 들어온다. DB만 새어 나가도 이메일 옆에서 읽히는 값이다. 여기에 한 줄을 더하는 일이 그 결정을 하는 자리다.
// 더하기 전에 묻는다: SQL이 이 값을 읽어야 하는가(CHECK, 유일 키, 인덱스, 조건절)? 아니라면 암호화한다.
var plaintextColumns = map[string]string{
	// 계정. 로그인과 알림이 이 값으로 사용자를 찾고 시각을 계산한다.
	"users.email":                     "로그인할 때 이 값으로 계정을 찾는다",
	"users.password_hash":             "되돌릴 수 없는 해시다",
	"users.display_name":              "부를 이름이다. 기록이 아니다",
	"users.timezone":                  "기록 날짜와 알림 시각을 계산한다",
	"users.role":                      "권한을 가른다",
	"users.is_demo":                   "시연용 계정을 가른다",
	"user_identities.provider":        "바깥 계정으로 사용자를 찾는다",
	"user_identities.subject":         "바깥 계정으로 사용자를 찾는다",
	"user_identities.email":           "공급자가 알려준 주소다",
	"user_keys.kek_version":           "마스터 키를 바꿀 때 옮길 행을 찾는다",
	"sessions.user_agent":             "세션을 연 브라우저다. 사용자가 한 말이 아니다",
	"sessions.ip":                     "세션을 연 주소다. 사용자가 한 말이 아니다",
	"consents.kind":                   "어느 동의인지로 찾는다",
	"consents.version":                "어느 판에 동의했는지로 찾는다",
	"user_settings.reminder_enabled":  "설정이다. 기록이 아니다",
	"user_settings.reminder_time":     "설정이다. 기록이 아니다",
	"user_settings.default_mode":      "설정이다. 기록이 아니다",
	"user_settings.analysis_enabled":  "설정이다. 기록이 아니다",
	"user_settings.memory_enabled":    "설정이다. 기록이 아니다",
	"user_settings.mood_pick_enabled": "설정이다. 기록이 아니다",

	// 기록의 뼈대. 날짜와 순서, 진행 상태다.
	"days.record_date":                "하루에 한 행이라는 유일 키와 기간 조회가 읽는다",
	"self_checks.record_date":         "기간 조회가 읽는다",
	"conversations.status":            "끝난 대화에는 끝난 시각이 있어야 한다는 CHECK가 읽는다",
	"conversations.started_mode":      "음성으로 시작했는지 채팅으로 시작했는지다",
	"conversations.processing_status": "대화 뒤에 도는 작업의 진행 상태다. 사용자가 한 말이 아니다",
	"utterances.seq":                  "대화 안의 순서이고 유일 키다",
	"utterances.speaker":              "누가 한 말인지다. CHECK가 읽는다",
	"utterances.modality":             "말로 했는지 글로 했는지다",
	"utterances.origin":               "말을 만든 쪽이다. CHECK가 읽는다",
	"utterances.stt_min_confidence":   "음성 인식의 품질 값이다. 말의 내용이 아니다",
	"diaries.status":                  "확인한 일기의 CHECK가 읽는다",
	"signals.extractor_version":       "추출 방식을 바꾼 앞뒤를 견준다. 사용자에 관한 값이 아니다",
	"gate_events.ai_failed":           "판별이 실패한 비율을 본다. 사용자에 관한 값이 아니다",
	"gate_events.ai_latency_ms":       "판별에 걸린 시간을 본다. 사용자에 관한 값이 아니다",

	// 글에서 나온 판단 값. 마음 상태에 관한 값인 줄 알면서 평문으로 둔 것들이다. 까닭은 계정 스키마의 머리말에 적혀 있다.
	// DB의 디스크와 백업을 민감한 정보로 다뤄야 하는 이유가 이 묶음이다.
	"conversations.end_reason":  "끝난 이유다. 위기로 끝났는지가 드러난다. CHECK가 읽는다",
	"conversations.check_state": "애매한 표현을 되물었는지, 직접 물었는지다. 앞으로만 가게 하는 조건절과 CHECK가 읽는다",
	"gate_events.rule_stage":    "규칙이 본 단계다",
	"gate_events.ai_stage":      "AI 판별이 본 단계다",
	"gate_events.final_stage":   "최근에 단계가 오른 발화를 세는 부분 인덱스가 읽는다",
	"gate_events.detected_by":   "어느 쪽이 알아챘는지다",
	"gate_events.adjustments":   "단계를 바꾼 규칙의 이름이다",
	"signals.item":              "같은 대화에서 같은 항목이 두 번 쌓이지 않게 하는 유일 키다",
	"signals.status":            "근거 없는 판단을 막는 CHECK가 읽는다",
	"signals.explicitness":      "근거 없는 판단을 막는 CHECK가 읽는다",
	"memories.kind":             "기억의 종류다. 걱정거리인지가 드러난다",
	"memories.sensitive":        "힘들어했던 일이라는 표시다. AI가 먼저 꺼내지 않게 거르는 데 쓴다",
	"memories.due_date":         "다가오는 일의 날짜다. 지나간 뒤에 안부를 물을 때 쓴다",
}

// 아래 시험은 지금의 테이블이 아니라 규칙을 확인한다. 뒤에 더해지는 마이그레이션도 같은 규칙으로 걸러진다.
func TestSchemaConventions(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)

	t.Run("평문으로 값을 담는 컬럼은 그렇게 두기로 정한 것뿐이다", func(t *testing.T) {
		columns := queryStrings(t, pool, `
			SELECT table_name || '.' || column_name
			FROM information_schema.columns
			WHERE `+appTableFilter+`
			  AND data_type NOT IN ('uuid', 'timestamp with time zone', 'bytea')
			ORDER BY 1`)
		require.NotEmpty(t, columns)

		// 목록 전체를 찍는 단언을 쓰지 않는다. 걸린 컬럼 하나가 수십 줄의 출력에 묻힌다.
		existing := make(map[string]struct{}, len(columns))
		for _, column := range columns {
			existing[column] = struct{}{}
			_, allowed := plaintextColumns[column]
			assert.True(t, allowed,
				"%s: 평문 컬럼이 새로 생겼다. SQL이 읽어야 하는 값이 아니면 암호화하고(_enc), 읽어야 하면 까닭과 함께 목록에 올린다", column)
		}
		for column := range plaintextColumns {
			// 없어진 컬럼이 목록에 남아 있으면, 같은 이름으로 다시 생긴 컬럼이 따져 보는 일 없이 통과한다.
			_, found := existing[column]
			assert.True(t, found, "%s: 스키마에 없는 컬럼이 목록에 남아 있다", column)
		}
	})

	t.Run("직접 고른 기분과 검진의 계기는 평문으로 두지 않는다", func(t *testing.T) {
		// 둘 다 SQL이 읽을 일이 없는 값인데 평문이면 날짜와 함께 마음 상태가 드러난다. 되돌아가지 않게 이름으로 못 박아 둔다.
		offenders := queryStrings(t, pool, `
			SELECT table_name || '.' || column_name
			FROM information_schema.columns
			WHERE `+appTableFilter+`
			  AND ((table_name = 'mood_picks' AND column_name = 'value')
			    OR (table_name = 'self_checks' AND column_name = 'trigger'))`)
		assert.Empty(t, offenders)
	})

	t.Run("ID와 시각 컬럼에는 DB 기본값이 없다", func(t *testing.T) {
		// ID는 암호문에 묶이므로 행을 쓰기 전에 정해져야 하고, 시각은 주입받은 시계에서 와야 한다.
		offenders := queryStrings(t, pool, `
			SELECT table_name || '.' || column_name
			FROM information_schema.columns
			WHERE `+appTableFilter+`
			  AND column_default IS NOT NULL
			  AND data_type IN ('uuid', 'timestamp with time zone', 'timestamp without time zone')`)
		assert.Empty(t, offenders)
	})

	t.Run("시각은 모두 timestamptz다", func(t *testing.T) {
		offenders := queryStrings(t, pool, `
			SELECT table_name || '.' || column_name
			FROM information_schema.columns
			WHERE `+appTableFilter+` AND data_type = 'timestamp without time zone'`)
		assert.Empty(t, offenders)
	})

	t.Run("_enc로 끝나는 컬럼은 모두 bytea다", func(t *testing.T) {
		offenders := queryStrings(t, pool, `
			SELECT table_name || '.' || column_name
			FROM information_schema.columns
			WHERE `+appTableFilter+` AND column_name LIKE '%\_enc' AND data_type <> 'bytea'`)
		assert.Empty(t, offenders)
	})

	t.Run("열거 값에 enum 타입을 쓰지 않는다", func(t *testing.T) {
		offenders := queryStrings(t, pool, `
			SELECT table_name || '.' || column_name
			FROM information_schema.columns
			WHERE `+appTableFilter+` AND data_type = 'USER-DEFINED' AND udt_name <> 'citext'`)
		assert.Empty(t, offenders)
	})

	t.Run("지운 표시를 남기는 컬럼이 없다", func(t *testing.T) {
		offenders := queryStrings(t, pool, `
			SELECT table_name || '.' || column_name
			FROM information_schema.columns
			WHERE `+appTableFilter+` AND column_name IN ('deleted_at', 'is_deleted', 'deleted')`)
		assert.Empty(t, offenders)
	})

	t.Run("외래 키는 모두 부모가 지워질 때의 동작을 정해 두었다", func(t *testing.T) {
		// 'a'는 NO ACTION이다. 그대로 두면 부모를 지우는 일이 자식 때문에 막혀 전체 삭제가 실패한다.
		offenders := queryStrings(t, pool, `
			SELECT conrelid::regclass::text || '.' || conname
			FROM pg_constraint
			WHERE contype = 'f'
			  AND connamespace = 'public'::regnamespace
			  AND conrelid::regclass::text NOT LIKE 'river%'
			  AND confdeltype NOT IN ('c', 'n')`)
		assert.Empty(t, offenders)
	})

	t.Run("외래 키 컬럼마다 그 컬럼으로 시작하는 인덱스가 있다", func(t *testing.T) {
		// 없으면 부모 행 하나를 지울 때마다 자식 테이블 전체를 훑는다. 부분 인덱스는 이 용도로 쓰이지 못한다.
		offenders := queryStrings(t, pool, `
			SELECT c.conrelid::regclass::text || '.' || c.conname
			FROM pg_constraint AS c
			WHERE c.contype = 'f'
			  AND c.connamespace = 'public'::regnamespace
			  AND c.conrelid::regclass::text NOT LIKE 'river%'
			  AND NOT EXISTS (
			      SELECT 1
			      FROM pg_index AS i
			      WHERE i.indrelid = c.conrelid
			        AND i.indpred IS NULL
			        AND i.indkey[0] = ANY (c.conkey)
			  )`)
		assert.Empty(t, offenders)
	})
}
