package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sirin-interact/tmrlife/server/internal/testdb"
)

// syncBuffer는 서버가 쓰는 동안 시험이 읽어도 되는 출력 버퍼다.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// 로그 한 줄에서 서버가 실제로 잡은 주소를 읽는다. msg와 addr 사이에는 다른 속성이 끼어 있다.
var listeningPattern = regexp.MustCompile(`"msg":"http server listening"[^\n]*"addr":"([^"]+)"`)

func TestRun_Serve(t *testing.T) {
	t.Run("설정에서 기대는 것을 모두 만들어 뜨고, 종료 신호에 깨끗하게 내려간다", func(t *testing.T) {
		pool := testdb.New(t)
		t.Setenv("DATABASE_URL", pool.Config().ConnString())
		t.Setenv("DATA_KEK_V1", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x2a}, 32)))
		t.Setenv("APP_ENV", "test")
		t.Setenv("LOG_LEVEL", "info")
		// 빈 포트를 운영체제가 고르게 한다.
		t.Setenv("HTTP_ADDR", "127.0.0.1:0")
		// 설정이 받아 주는 가장 싼 값이다. 뜰 때 해시를 한 번 계산한다.
		t.Setenv("PASSWORD_ARGON2_MEMORY_KIB", "7168")
		t.Setenv("PASSWORD_ARGON2_TIME", "1")

		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		stdout, stderr := &syncBuffer{}, &syncBuffer{}
		exit := make(chan int, 1)
		go func() { exit <- run(ctx, []string{"serve"}, stdout, stderr) }()

		var addr string
		require.Eventually(t, func() bool {
			if m := listeningPattern.FindStringSubmatch(stdout.String()); m != nil {
				addr = m[1]
				return true
			}
			return false
		}, 30*time.Second, 50*time.Millisecond, "서버가 뜨지 않았다: %s%s", stdout, stderr)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/readyz", nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusOK, resp.StatusCode, "DB에 닿으므로 준비된 상태여야 한다")

		// 실제 소켓으로 가입하고, 받은 쿠키로 내 정보를 읽는다. 경로가 실행 파일까지 이어져 있는지 본다.
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		client := &http.Client{Jar: jar}

		req, err = http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/v1/auth/requirements", nil)
		require.NoError(t, err)
		resp, err = client.Do(req)
		require.NoError(t, err)
		var requirements struct {
			Consents []map[string]string `json:"consents"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&requirements))
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NotEmpty(t, requirements.Consents)

		signup, err := json.Marshal(map[string]any{
			"email":    "mina@example.com",
			"password": "오늘도 수고했어요, 내일 봐요",
			"consents": requirements.Consents,
		})
		require.NoError(t, err)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/api/v1/auth/signup", bytes.NewReader(signup))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		req, err = http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/v1/me", nil)
		require.NoError(t, err)
		resp, err = client.Do(req)
		require.NoError(t, err)
		var me struct {
			User struct {
				Email string `json:"email"`
			} `json:"user"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&me))
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "mina@example.com", me.User.Email)

		stop()
		select {
		case code := <-exit:
			assert.Equal(t, exitOK, code)
		case <-time.After(30 * time.Second):
			t.Fatal("서버가 내려가지 않았다")
		}

		logs := stdout.String()
		assert.Contains(t, logs, `"app":"naeil-server"`)
		assert.Contains(t, logs, `"msg":"dependencies ready"`)
		assert.Contains(t, logs, `"msg":"http server stopped"`)
		assert.NotContains(t, logs, "KioqKioq", "마스터 키가 로그에 나오면 안 된다")
		assert.Contains(t, logs, `"msg":"user signed up"`)
		assert.NotContains(t, logs, "mina@example.com", "이메일이 로그에 나오면 안 된다")
		assert.NotContains(t, logs, "수고했어요", "비밀번호가 로그에 나오면 안 된다")
		assert.Empty(t, stderr.String())
	})
}

func TestRun_Usage(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{"모르는 명령은 사용법을 보여주고 2로 끝난다", []string{"launch"}, exitUsage, "unknown command"},
		{"migrate에 동작을 주지 않으면 사용법을 보여준다", []string{"migrate"}, exitUsage, "usage:"},
		{"migrate에 모르는 동작을 주면 사용법을 보여준다", []string{"migrate", "sideways"}, exitUsage, "unknown migrate action"},
		{"serve에 군더더기 인자를 주면 사용법을 보여준다", []string{"serve", "now"}, exitUsage, "usage:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(t.Context(), tt.args, &stdout, &stderr)
			assert.Equal(t, tt.wantCode, code)
			assert.Contains(t, stderr.String(), tt.wantStderr)
		})
	}

	t.Run("help는 사용법을 stdout에 보여주고 0으로 끝난다", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		assert.Equal(t, exitOK, run(t.Context(), []string{"help"}, &stdout, &stderr))
		assert.Contains(t, stdout.String(), "migrate up|down|status")
		assert.Empty(t, stderr.String())
	})
}

func TestRun_ConfigErrors(t *testing.T) {
	t.Run("설정이 틀리면 서버를 띄우지 않고 변수 이름만 알려준다", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://app:s3cr3t-pass@db.internal:5432/app")
		t.Setenv("DATA_KEK_V1", "not-a-valid-key-but-still-secret")
		t.Setenv("APP_ENV", "dev")

		var stdout, stderr bytes.Buffer
		code := run(t.Context(), nil, &stdout, &stderr)

		assert.Equal(t, exitUsage, code)
		assert.Contains(t, stderr.String(), "DATA_KEK_V1")
		assert.NotContains(t, stderr.String(), "not-a-valid-key-but-still-secret")
		assert.NotContains(t, stderr.String(), "s3cr3t-pass")
		assert.Empty(t, stdout.String())
	})

	t.Run("migrate는 DB 주소가 없으면 변수 이름을 알려준다", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")

		var stdout, stderr bytes.Buffer
		code := run(t.Context(), []string{"migrate", "status"}, &stdout, &stderr)

		assert.Equal(t, exitUsage, code)
		assert.Contains(t, stderr.String(), "DATABASE_URL")
	})
}

func TestRun_Migrate(t *testing.T) {
	t.Run("마스터 키 없이도 올리고, 상태를 보고, 내릴 수 있다", func(t *testing.T) {
		pool := testdb.NewEmpty(t)
		t.Setenv("DATABASE_URL", pool.Config().ConnString())
		t.Setenv("DATA_KEK_V1", "")
		t.Setenv("LOG_LEVEL", "info")
		// 시험을 돌리는 환경에 어떤 값이 있든 같은 결과가 나오게 고정한다.
		t.Setenv("APP_ENV", "test")
		t.Setenv("MIGRATE_ALLOW_DOWN", "true")

		var stdout, stderr bytes.Buffer
		require.Equal(t, exitOK, run(t.Context(), []string{"migrate", "up"}, &stdout, &stderr), stderr.String())
		assert.Contains(t, stderr.String(), "migrations applied")
		assert.Empty(t, stdout.String(), "진행 로그는 stderr로만 나간다")

		stdout.Reset()
		stderr.Reset()
		require.Equal(t, exitOK, run(t.Context(), []string{"migrate", "status"}, &stdout, &stderr), stderr.String())
		assert.Contains(t, stdout.String(), "00001_extensions.sql")
		assert.Contains(t, stdout.String(), "applied")
		assert.NotContains(t, stdout.String(), "pending")

		stdout.Reset()
		stderr.Reset()
		require.Equal(t, exitOK, run(t.Context(), []string{"migrate", "down"}, &stdout, &stderr), stderr.String())
		assert.Contains(t, stderr.String(), "migration rolled back")
	})

	// 닿을 수 없는 주소다. 거부가 DB에 붙기 전에 일어난다면 연결 오류(1)가 아니라 사용법 오류(2)로 끝난다.
	const unreachable = "postgres://app:s3cr3t-pass@127.0.0.1:1/app?sslmode=disable&connect_timeout=2"

	refused := []struct {
		name       string
		env        map[string]string
		wantStderr string
	}{
		{"허락 없이는 내리지 않는다", map[string]string{"APP_ENV": "dev", "MIGRATE_ALLOW_DOWN": ""}, "MIGRATE_ALLOW_DOWN=true"},
		{"APP_ENV가 비어 있어도 허락으로 치지 않는다", map[string]string{"APP_ENV": "", "MIGRATE_ALLOW_DOWN": ""}, "MIGRATE_ALLOW_DOWN=true"},
		{"허락이 거짓이면 내리지 않는다", map[string]string{"APP_ENV": "dev", "MIGRATE_ALLOW_DOWN": "false"}, "MIGRATE_ALLOW_DOWN=true"},
		{"운영에서는 허락이 있어도 내리지 않는다", map[string]string{"APP_ENV": "prod", "MIGRATE_ALLOW_DOWN": "true"}, "APP_ENV=prod"},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", unreachable)
			for name, value := range tt.env {
				t.Setenv(name, value)
			}

			var stdout, stderr bytes.Buffer
			code := run(t.Context(), []string{"migrate", "down"}, &stdout, &stderr)

			assert.Equal(t, exitUsage, code, "DB에 붙어 보기 전에 거부해야 한다: %s", stderr.String())
			assert.Contains(t, stderr.String(), tt.wantStderr)
			assert.NotContains(t, stderr.String(), "open database pool")
			assert.NotContains(t, stderr.String(), "migration failed")
			assert.NotContains(t, stderr.String(), "s3cr3t-pass")
		})
	}

	t.Run("올리기와 상태 보기는 허락 없이도, 운영에서도 된다", func(t *testing.T) {
		pool := testdb.NewEmpty(t)
		t.Setenv("DATABASE_URL", pool.Config().ConnString())
		t.Setenv("APP_ENV", "prod")
		t.Setenv("MIGRATE_ALLOW_DOWN", "")

		var stdout, stderr bytes.Buffer
		require.Equal(t, exitOK, run(t.Context(), []string{"migrate", "up"}, &stdout, &stderr), stderr.String())
		require.Equal(t, exitOK, run(t.Context(), []string{"migrate", "status"}, &stdout, &stderr), stderr.String())
	})

	t.Run("DB에 닿지 않으면 1로 끝나고 비밀번호는 로그에 남지 않는다", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://app:s3cr3t-pass@127.0.0.1:1/app?sslmode=disable&connect_timeout=2")

		var stdout, stderr bytes.Buffer
		code := run(t.Context(), []string{"migrate", "up"}, &stdout, &stderr)

		assert.Equal(t, exitFailure, code)
		assert.Contains(t, stderr.String(), "migration failed")
		assert.NotContains(t, stderr.String(), "s3cr3t-pass")
	})
}
